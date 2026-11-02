package study

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"
)

// SweepCollectionPlans can be run concurrently by several server instances.
// The scan is a hint; all eligibility, dedupe, task creation and cursor updates
// are rechecked inside the existing serialized write transaction.
func (s *Service) SweepCollectionPlans(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, ErrInvalid
	}
	now := s.Now().UTC()
	plans := []collectionPlanRecord{}
	queues := []CollectionTask{}
	err := s.Store.View(WithWorkspace(ctx, ""), func(tx Tx) error {
		if sql, ok := tx.(interface {
			collectionDuePlans(time.Time, int) ([]collectionPlanRecord, error)
			collectionExpiredTasks(time.Time, int) ([]CollectionTask, error)
		}); ok {
			var e error
			plans, e = sql.collectionDuePlans(now, limit)
			if e != nil {
				return e
			}
			queues, e = sql.collectionExpiredTasks(now, limit)
			return e
		}
		all, e := recordsAs[collectionPlanRecord](tx, collectionPlanKind)
		if e != nil {
			return e
		}
		eligible := map[string]bool{}
		ownerEnabled := func(id string) bool {
			if id == "" {
				return false
			}
			if yes, ok := eligible[id]; ok {
				return yes
			}
			user, e := workspaceUserByID(tx, id)
			yes := e == nil && user.Enabled
			eligible[id] = yes
			return yes
		}
		for _, rec := range all {
			if rec.Enabled && rec.DeletedAt == nil && rec.NextRunAt != nil && !rec.NextRunAt.After(now) && ownerEnabled(rec.OwnerID) {
				plans = append(plans, rec)
			}
		}
		tasks, e := recordsAs[CollectionTask](tx, "agent_task")
		if e != nil {
			return e
		}
		for _, task := range tasks {
			if task.PlanID == "" || !ownerEnabled(task.OwnerID) {
				continue
			}
			if _, changed := refreshCollectionQueueTask(task, now); changed {
				queues = append(queues, task)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// Reclaim queues even for manual/one-off plans that have no next_run_at.
	sort.Slice(queues, func(i, j int) bool { return queues[i].CreatedAt.Before(queues[j].CreatedAt) })
	for _, task := range queues[:min(limit, len(queues))] {
		scoped := WithWorkspace(ctx, task.OwnerID)
		if err := s.Store.Update(scoped, func(tx Tx) error {
			current, e := recordAs[CollectionTask](tx, "agent_task", task.ID)
			if errors.Is(e, ErrNotFound) {
				return nil
			}
			if e != nil {
				return e
			}
			updated, changed := refreshCollectionQueueTask(current, now)
			if !changed {
				return nil
			}
			return recordSave(tx, "agent_task", updated.ID, updated)
		}); err != nil && !errors.Is(err, ErrUnauthorized) {
			return 0, err
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].NextRunAt.Equal(*plans[j].NextRunAt) {
			return plans[i].ID < plans[j].ID
		}
		return plans[i].NextRunAt.Before(*plans[j].NextRunAt)
	})
	processed := 0
	for _, candidate := range plans[:min(limit, len(plans))] {
		scoped := WithWorkspace(ctx, candidate.OwnerID)
		created := false
		err := s.Store.Update(scoped, func(tx Tx) error {
			rec, e := recordAs[collectionPlanRecord](tx, collectionPlanKind, candidate.ID)
			if errors.Is(e, ErrNotFound) {
				return nil
			}
			if e != nil {
				return e
			}
			if !rec.Enabled || rec.DeletedAt != nil || rec.NextRunAt == nil || rec.NextRunAt.After(now) {
				return nil
			}
			slot := latestCollectionSlot(rec.Schedule, *rec.NextRunAt, now)
			check, e := evaluateCollectionUnread(tx, rec.OwnerID, rec.Schedule, now)
			if e != nil {
				return e
			}
			if check != nil {
				rec.LastUnreadCheck = check
				if !check.Matched {
					// A false poll is an observation, not a fabricated run. Still
					// persist the cursor so every 15s worker tick cannot recheck it.
					rec.NextRunAt = nextCollectionTime(rec.Schedule, slot)
					return recordSave(tx, collectionPlanKind, rec.ID, rec)
				}
			}
			reason := ""
			if now.Sub(slot) > time.Duration(rec.CatchUpSeconds)*time.Second {
				reason = "misfire_window_exceeded"
			}
			input := CollectionPlanTrigger{EventID: slot.UTC().Format(time.RFC3339)}
			source := "schedule"
			if check != nil {
				source = "condition"
			}
			_, created, e = s.dispatchCollectionPlanTx(scoped, tx, rec, source, input, slot, now, reason, check)
			if e != nil {
				return e
			}
			rec.NextRunAt = nextCollectionTime(rec.Schedule, slot)
			rec.LastRunAt = &now
			return recordSave(tx, collectionPlanKind, rec.ID, rec)
		})
		if errors.Is(err, ErrUnauthorized) {
			continue
		}
		if err != nil {
			return processed, err
		}
		if created {
			processed++
		}
	}
	return processed, nil
}

func (s *Service) RunCollectionScheduler(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.SweepCollectionPlans(ctx, 100); err != nil && ctx.Err() == nil {
			logger.Error("collection schedule sweep failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
