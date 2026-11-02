package study

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const collectionPlanKind = "agent_collection_plan"
const collectionRunKind = "agent_collection_run"

var ErrTriggerRateLimited = errors.New("trigger_rate_limited")

// CollectionPlan owns a snapshot, never a live reference to a task template.
type CollectionPlan struct {
	ID              string                 `json:"id"`
	OwnerID         string                 `json:"owner_id"`
	Name            string                 `json:"name"`
	Task            CollectionTaskInput    `json:"task"`
	Schedule        CollectionSchedule     `json:"schedule"`
	Enabled         bool                   `json:"enabled"`
	QueueTTLSeconds int                    `json:"queue_ttl_seconds"`
	CatchUpSeconds  int                    `json:"catch_up_seconds"`
	AllowURL        bool                   `json:"allow_url"`
	AllowKeyword    bool                   `json:"allow_keyword"`
	TriggerKeySet   bool                   `json:"trigger_key_set"`
	Revision        int64                  `json:"revision"`
	NextRunAt       *time.Time             `json:"next_run_at,omitempty"`
	LastRunAt       *time.Time             `json:"last_run_at,omitempty"`
	LastUnreadCheck *CollectionUnreadCheck `json:"last_unread_check,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	DeletedAt       *time.Time             `json:"deleted_at,omitempty"`
}

type CollectionPlanInput struct {
	Name            string              `json:"name"`
	Task            CollectionTaskInput `json:"task"`
	Schedule        CollectionSchedule  `json:"schedule"`
	Enabled         bool                `json:"enabled"`
	QueueTTLSeconds int                 `json:"queue_ttl_seconds"`
	CatchUpSeconds  int                 `json:"catch_up_seconds"`
	AllowURL        bool                `json:"allow_url"`
	AllowKeyword    bool                `json:"allow_keyword"`
}

type collectionPlanInternal struct {
	TriggerKeyHash string     `json:"trigger_key_hash,omitempty"`
	LastWebhookAt  *time.Time `json:"last_webhook_at,omitempty"`
}
type collectionPlanRecord struct {
	CollectionPlan
	Internal collectionPlanInternal `json:"internal"`
}

type CollectionPlanTrigger struct {
	EventID string     `json:"event_id"`
	SentAt  *time.Time `json:"sent_at,omitempty"`
	URL     string     `json:"url,omitempty"`
	Keyword string     `json:"keyword,omitempty"`
}

type CollectionPlanRun struct {
	ID             string                 `json:"id"`
	OwnerID        string                 `json:"owner_id"`
	PlanID         string                 `json:"plan_id"`
	PlanName       string                 `json:"plan_name"`
	PlanRevision   int64                  `json:"plan_revision"`
	TriggerSource  string                 `json:"trigger_source"`
	TriggerKey     string                 `json:"trigger_key"`
	RequestHash    string                 `json:"request_hash"`
	ScheduledFor   time.Time              `json:"scheduled_for"`
	CreatedAt      time.Time              `json:"created_at"`
	DispatchStatus string                 `json:"dispatch_status"`
	Reason         string                 `json:"reason,omitempty"`
	TaskID         string                 `json:"task_id,omitempty"`
	TaskConfig     CollectionTaskInput    `json:"task_config"`
	TaskStatus     string                 `json:"task_status,omitempty"`
	TaskDeleted    bool                   `json:"task_deleted,omitempty"`
	Stats          TaskStats              `json:"stats"`
	Result         *TaskResult            `json:"result,omitempty"`
	FinishedAt     *time.Time             `json:"finished_at,omitempty"`
	UnreadCheck    *CollectionUnreadCheck `json:"unread_check,omitempty"`
}

func (s *Service) prepareCollectionPlan(ctx context.Context, in CollectionPlanInput) (CollectionPlanInput, error) {
	if WorkspaceUserID(ctx) == "" {
		return in, ErrUnauthorized
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validText(in.Name, 120) {
		return in, ErrInvalid
	}
	var err error
	in.Schedule, err = normalizeCollectionSchedule(in.Schedule)
	if err != nil {
		return in, err
	}
	if in.QueueTTLSeconds == 0 {
		in.QueueTTLSeconds = 3600
	}
	if in.CatchUpSeconds == 0 {
		in.CatchUpSeconds = 3600
	}
	if in.QueueTTLSeconds < 60 || in.QueueTTLSeconds > 604800 || in.CatchUpSeconds < 30 || in.CatchUpSeconds > 604800 {
		return in, ErrInvalid
	}
	if in.Schedule.Kind != "webhook" && (in.AllowURL || in.AllowKeyword) {
		return in, ErrInvalid
	}
	if !validText(collectionPlanGoal(in.Task.Goal, s.Now().UTC(), CollectionPlanTrigger{}), maximumTaskGoalBytes) {
		return in, ErrInvalid
	}
	in.Task, err = normalizeTaskInput(in.Task, s.agentPolicy())
	if err != nil {
		return in, err
	}
	if in.Task.SourceID == "" {
		source, e := s.EnsureAgentCollectionSource(ctx)
		if e != nil {
			return in, e
		}
		in.Task.SourceID = source.ID
	}
	return in, nil
}

func validateCollectionTaskBinding(tx Tx, in CollectionTaskInput) error {
	source, err := tx.Source(in.SourceID)
	if err != nil {
		return err
	}
	if !source.Enabled || !source.Allows(PermissionIngest) {
		return ErrDisabled
	}
	if in.AgentID != "" {
		agent, err := recordAs[agentRecord](tx, "agent", in.AgentID)
		if err != nil {
			return err
		}
		if !agent.Agent.Enabled || !agent.Agent.Allows(PermissionTaskClaim) {
			return ErrDisabled
		}
	}
	return nil
}

// The caller owns the transaction. No nested Store.Update and no network I/O.
func createCollectionTaskTx(ctx context.Context, tx Tx, in CollectionTaskInput, now time.Time) (CollectionTask, error) {
	if err := validateCollectionTaskBinding(tx, in); err != nil {
		return CollectionTask{}, err
	}
	out := CollectionTask{OwnerID: WorkspaceUserID(ctx), ID: uuid.NewString(), SourceID: in.SourceID,
		Title: in.Title, Goal: in.Goal, Constraints: in.Constraints, Budget: in.Budget,
		Status: TaskStatusPending, Priority: in.Priority, MaxAttempts: in.MaxAttempts,
		AssignedAgentID: in.AgentID, CreatedAt: now, UpdatedAt: now}
	return out, recordSave(tx, "agent_task", out.ID, out)
}

func planFromInput(in CollectionPlanInput) CollectionPlan {
	return CollectionPlan{Name: in.Name, Task: in.Task, Schedule: in.Schedule, Enabled: in.Enabled,
		QueueTTLSeconds: in.QueueTTLSeconds, CatchUpSeconds: in.CatchUpSeconds, AllowURL: in.AllowURL, AllowKeyword: in.AllowKeyword}
}
func sameCollectionSchedule(a, b CollectionSchedule) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s *Service) CreateCollectionPlan(ctx context.Context, in CollectionPlanInput) (out CollectionPlan, err error) {
	in, err = s.prepareCollectionPlan(ctx, in)
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		if err := validateCollectionTaskBinding(tx, in.Task); err != nil {
			return err
		}
		all, err := recordsAs[collectionPlanRecord](tx, collectionPlanKind)
		if err != nil {
			return err
		}
		count := 0
		for _, p := range all {
			if p.DeletedAt == nil {
				count++
			}
		}
		if count >= 100 {
			return ErrQuota
		}
		now := s.Now().UTC()
		if in.Enabled && in.Schedule.Kind == "once" && !in.Schedule.At.After(now) {
			return ErrInvalid
		}
		out = planFromInput(in)
		out.ID = uuid.NewString()
		out.OwnerID = WorkspaceUserID(ctx)
		out.Revision = 1
		out.CreatedAt = now
		out.UpdatedAt = now
		if out.Enabled {
			out.NextRunAt = nextCollectionTime(out.Schedule, now)
		}
		return recordSave(tx, collectionPlanKind, out.ID, collectionPlanRecord{CollectionPlan: out})
	})
	return
}

func (s *Service) CollectionPlans(ctx context.Context, includeDeleted bool) (out []CollectionPlan, err error) {
	if WorkspaceUserID(ctx) == "" {
		return nil, ErrUnauthorized
	}
	out = []CollectionPlan{}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, err := recordsAs[collectionPlanRecord](tx, collectionPlanKind)
		if err != nil {
			return err
		}
		for _, p := range all {
			if includeDeleted || p.DeletedAt == nil {
				out = append(out, p.CollectionPlan)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
				return out[i].ID < out[j].ID
			}
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		})
		return nil
	})
	return
}

func (s *Service) CollectionPlan(ctx context.Context, id string) (out CollectionPlan, err error) {
	if WorkspaceUserID(ctx) == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		rec, e := recordAs[collectionPlanRecord](tx, collectionPlanKind, id)
		out = rec.CollectionPlan
		return e
	})
	return
}

func (s *Service) UpdateCollectionPlan(ctx context.Context, id string, revision int64, in CollectionPlanInput) (out CollectionPlan, err error) {
	in, err = s.prepareCollectionPlan(ctx, in)
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		rec, err := recordAs[collectionPlanRecord](tx, collectionPlanKind, id)
		if err != nil {
			return err
		}
		if rec.DeletedAt != nil {
			return ErrNotFound
		}
		if rec.Revision != revision {
			return ErrConflict
		}
		if err := validateCollectionTaskBinding(tx, in.Task); err != nil {
			return err
		}
		now := s.Now().UTC()
		out = planFromInput(in)
		out.ID = rec.ID
		out.OwnerID = rec.OwnerID
		out.CreatedAt = rec.CreatedAt
		out.UpdatedAt = now
		out.Revision = rec.Revision + 1
		out.LastRunAt = rec.LastRunAt
		if sameCollectionSchedule(rec.Schedule, out.Schedule) {
			out.LastUnreadCheck = rec.LastUnreadCheck
		}
		if out.Enabled {
			if rec.Enabled && sameCollectionSchedule(rec.Schedule, out.Schedule) {
				out.NextRunAt = rec.NextRunAt
			} else {
				if out.Schedule.Kind == "once" && !out.Schedule.At.After(now) {
					return ErrInvalid
				}
				out.NextRunAt = nextCollectionTime(out.Schedule, now)
			}
		}
		if out.Schedule.Kind != "webhook" {
			rec.Internal = collectionPlanInternal{}
		}
		out.TriggerKeySet = rec.Internal.TriggerKeyHash != ""
		rec.CollectionPlan = out
		return recordSave(tx, collectionPlanKind, id, rec)
	})
	return
}

// Archive rather than cascading deletes. Existing tasks continue independently.
func (s *Service) DeleteCollectionPlan(ctx context.Context, id string, revision int64) (out CollectionPlan, err error) {
	if WorkspaceUserID(ctx) == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		rec, e := recordAs[collectionPlanRecord](tx, collectionPlanKind, id)
		if e != nil {
			return e
		}
		if rec.DeletedAt != nil {
			return ErrNotFound
		}
		if rec.Revision != revision {
			return ErrConflict
		}
		now := s.Now().UTC()
		rec.Enabled = false
		rec.NextRunAt = nil
		rec.DeletedAt = &now
		rec.UpdatedAt = now
		rec.Revision++
		rec.TriggerKeySet = false
		rec.Internal = collectionPlanInternal{}
		out = rec.CollectionPlan
		return recordSave(tx, collectionPlanKind, id, rec)
	})
	return
}

func (s *Service) RotateCollectionTriggerKey(ctx context.Context, id string, revision int64, revoke bool) (out CollectionPlan, key string, err error) {
	if WorkspaceUserID(ctx) == "" {
		return out, "", ErrUnauthorized
	}
	if !revoke {
		key, err = NewKey()
		if err != nil {
			return
		}
		key = "wztg_" + WorkspaceUserID(ctx) + "_" + strings.TrimPrefix(key, "wzsk_")
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		rec, e := recordAs[collectionPlanRecord](tx, collectionPlanKind, id)
		if e != nil {
			return e
		}
		if rec.DeletedAt != nil {
			return ErrNotFound
		}
		if rec.Revision != revision {
			return ErrConflict
		}
		if rec.Schedule.Kind != "webhook" {
			return ErrInvalid
		}
		rec.Internal = collectionPlanInternal{}
		if !revoke {
			rec.Internal.TriggerKeyHash = HashKey(key)
		}
		rec.TriggerKeySet = !revoke
		rec.Revision++
		rec.UpdatedAt = s.Now().UTC()
		out = rec.CollectionPlan
		return recordSave(tx, collectionPlanKind, id, rec)
	})
	if err != nil {
		key = ""
	}
	return
}

// Save a terminal summary before the user deletes the original task/events.
func snapshotCollectionRunTx(tx Tx, task CollectionTask) error {
	if task.PlanRunID == "" {
		return nil
	}
	run, err := recordAs[CollectionPlanRun](tx, collectionRunKind, task.PlanRunID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	run.TaskStatus = task.Status
	run.Stats = task.Stats
	run.Result = task.Result
	run.FinishedAt = task.FinishedAt
	return recordSave(tx, collectionRunKind, run.ID, run)
}

func (s *Service) CollectionPlanRuns(ctx context.Context, id string, limit, offset int) (out []CollectionPlanRun, total int, err error) {
	if WorkspaceUserID(ctx) == "" {
		return nil, 0, ErrUnauthorized
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return nil, 0, ErrInvalid
	}
	out = []CollectionPlanRun{}
	err = s.Store.View(ctx, func(tx Tx) error {
		if _, err := recordAs[collectionPlanRecord](tx, collectionPlanKind, id); err != nil {
			return err
		}
		var e error
		out, total, e = collectionRunPage(tx, id, limit, offset)
		if e != nil {
			return e
		}
		for i := range out {
			if out[i].TaskID == "" {
				continue
			}
			task, e := recordAs[CollectionTask](tx, "agent_task", out[i].TaskID)
			if errors.Is(e, ErrNotFound) {
				out[i].TaskDeleted = true
				continue
			}
			if e != nil {
				return e
			}
			out[i].TaskStatus = task.Status
			out[i].Stats = task.Stats
			out[i].Result = task.Result
			out[i].FinishedAt = task.FinishedAt
		}
		return nil
	})
	return
}
