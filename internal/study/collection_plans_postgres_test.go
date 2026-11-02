package study

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresCollectionPlansConcurrentAndUniqueConstraint(t *testing.T) {
	s := fixture(t, "postgres")
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	user := User{ID: uuid.NewString(), Email: "pg-plans@example.com", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if e := s.Store.Update(ctx, func(tx Tx) error { return recordSave(tx, "learning_user", user.ID, userRecord{Profile: user}) }); e != nil {
		t.Fatal(e)
	}
	ctx = WithWorkspace(ctx, user.ID)
	if e := s.Store.Ping(ctx); e != nil {
		t.Fatal(e)
	}
	p := createPlan(t, s, ctx, "interval")
	now = now.Add(7*time.Hour + 10*time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.SweepCollectionPlans(context.Background(), 100); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	runs, total, e := s.CollectionPlanRuns(ctx, p.ID, 10, 0)
	if e != nil || total != 1 || runs[0].TaskID == "" {
		t.Fatal(runs, total, e)
	}
	// Prove the database rejects a second row with the same logical trigger,
	// even if some future caller accidentally uses a different run primary ID.
	duplicate := runs[0]
	duplicate.ID = uuid.NewString()
	e = s.Store.Update(ctx, func(tx Tx) error { return recordSave(tx, collectionRunKind, duplicate.ID, duplicate) })
	if !errors.Is(e, ErrConflict) {
		t.Fatal("unique trigger constraint missing", e)
	}
	// Terminal history does not count as active work in the SQL fast path.
	if _, e = s.CancelAgentTask(ctx, runs[0].TaskID, "test"); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.Update(ctx, func(tx Tx) error {
		for i := 0; i < 80; i++ {
			task := CollectionTask{ID: uuid.NewString(), OwnerID: user.ID, PlanID: p.ID, Status: TaskStatusSucceeded, CreatedAt: now}
			if e := recordSave(tx, "agent_task", task.ID, task); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	r := triggerPlan(t, s, ctx, p.ID, "after-terminal-history")
	if r.TaskID == "" {
		t.Fatal(r)
	}
	// Queue expiration also runs for manual plans with no next_run_at.
	manual := createPlan(t, s, ctx, "manual")
	queued := triggerPlan(t, s, ctx, manual.ID, "manual-queue")
	now = now.Add(2 * time.Hour)
	if _, e = s.SweepCollectionPlans(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	task, e := s.CollectionTask(ctx, queued.TaskID)
	if e != nil || task.Status != TaskStatusExpired {
		t.Fatal(task, e)
	}
	if _, e = s.DeleteCollectionTask(ctx, queued.TaskID); e != nil {
		t.Fatal(e)
	}
	runs, total, e = s.CollectionPlanRuns(ctx, manual.ID, 1, 0)
	if e != nil || total != 1 || runs[0].TaskStatus != TaskStatusExpired {
		t.Fatal(runs, total, e)
	}
	// Exercise actual PostgreSQL rollback after task creation but before run save.
	manual2 := createPlan(t, s, ctx, "manual")
	original := s.Store
	s.Store = collectionFailStore{original}
	if _, e = s.TriggerCollectionPlan(ctx, manual2.ID, CollectionPlanTrigger{EventID: "fail"}); e == nil {
		t.Fatal("expected injected error")
	}
	s.Store = original
	runs, total, e = s.CollectionPlanRuns(ctx, manual2.ID, 10, 0)
	if e != nil || total != 0 {
		t.Fatal(runs, total, e)
	}
	if e = s.Store.View(ctx, func(tx Tx) error {
		active, e := activeCollectionTasks(tx, manual2.ID)
		if e != nil {
			return e
		}
		if len(active) != 0 {
			t.Fatal("orphan tasks after rollback", active)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestPostgresCollectionPlansAdministratorAndDisabledOwner(t *testing.T) {
	s := fixture(t, "postgres")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	admin, _, e := s.EnsureAdministrator(context.Background(), AdministratorSettings{Email: "pg-plan-admin@example.com", Password: "postgres-plan-test-password"})
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := WithWorkspace(context.Background(), admin.ID)
	plan := createPlan(t, s, adminCtx, "interval")
	user := User{ID: uuid.NewString(), Email: "disabled-pg-plan@example.com", Enabled: true, Revision: 1}
	if e = s.Store.Update(context.Background(), func(tx Tx) error { return recordSave(tx, "learning_user", user.ID, userRecord{Profile: user}) }); e != nil {
		t.Fatal(e)
	}
	userCtx := WithWorkspace(context.Background(), user.ID)
	disabled := createPlan(t, s, userCtx, "interval")
	user.Enabled = false
	if e = s.Store.Update(context.Background(), func(tx Tx) error { return recordSave(tx, "learning_user", user.ID, userRecord{Profile: user}) }); e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Hour)
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	runs, total, e := s.CollectionPlanRuns(adminCtx, plan.ID, 10, 0)
	if e != nil || total != 1 || runs[0].TaskID == "" {
		t.Fatal(runs, total, e)
	}
	if e = s.Store.View(context.Background(), func(tx Tx) error {
		all, e := recordsAs[CollectionPlanRun](tx, collectionRunKind)
		if e != nil {
			return e
		}
		for _, r := range all {
			if r.PlanID == disabled.ID {
				t.Fatal("disabled owner dispatched", r)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
