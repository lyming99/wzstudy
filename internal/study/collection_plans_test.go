package study

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func planInput(kind string) CollectionPlanInput {
	return CollectionPlanInput{Name: "Daily sources", Task: CollectionTaskInput{Goal: "Collect official articles", Constraints: TaskConstraints{MaxItems: 3, AllowDomains: []string{"go.dev"}}}, Schedule: CollectionSchedule{Kind: kind, Timezone: "UTC", IntervalMinutes: 60}, Enabled: true}
}
func createPlan(t *testing.T, s *Service, ctx context.Context, kind string) CollectionPlan {
	t.Helper()
	in := planInput(kind)
	if kind != "interval" {
		in.Schedule.IntervalMinutes = 0
	}
	p, err := s.CreateCollectionPlan(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func triggerPlan(t *testing.T, s *Service, ctx context.Context, id, event string) CollectionPlanRun {
	t.Helper()
	r, err := s.TriggerCollectionPlan(ctx, id, CollectionPlanTrigger{EventID: event})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCollectionScheduleRules(t *testing.T) {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   CollectionSchedule
		want time.Time
	}{
		{"interval", CollectionSchedule{Kind: "interval", IntervalMinutes: 15}, ref.Add(15 * time.Minute)},
		{"daily zone", CollectionSchedule{Kind: "daily", Timezone: "Asia/Shanghai", TimeOfDay: "09:00"}, ref.Add(time.Hour)},
		{"weekly", CollectionSchedule{Kind: "weekly", Timezone: "UTC", TimeOfDay: "10:00", Weekdays: []int{int(ref.Weekday())}}, ref.Add(10 * time.Hour)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			times, e := PreviewCollectionSchedule(c.in, ref, 3)
			if e != nil || len(times) != 3 || !times[0].Equal(c.want) {
				t.Fatal(times, e)
			}
			for i := 1; i < len(times); i++ {
				if !times[i].After(times[i-1]) {
					t.Fatal(times)
				}
			}
		})
	}
	at := ref.Add(time.Minute)
	out, e := PreviewCollectionSchedule(CollectionSchedule{Kind: "once", At: &at}, ref, 5)
	if e != nil || len(out) != 1 || !out[0].Equal(at) {
		t.Fatal(out, e)
	}
	for _, in := range []CollectionSchedule{{Kind: "cron"}, {Kind: "interval", IntervalMinutes: 0}, {Kind: "daily", TimeOfDay: "9:00"}, {Kind: "weekly", TimeOfDay: "09:00", Weekdays: []int{7}}, {Kind: "weekly", TimeOfDay: "09:00", Weekdays: []int{1, 1}}, {Kind: "daily", TimeOfDay: "09:00", Timezone: "Unknown/Place"}, {Kind: "manual", At: &at}} {
		if _, e := PreviewCollectionSchedule(in, ref, 3); !errors.Is(e, ErrInvalid) {
			t.Fatalf("accepted invalid schedule: %+v %v", in, e)
		}
	}
}

func TestCollectionScheduleDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Spring gap: 02:30 does not exist, so March 8 is skipped.
	before := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	out, e := PreviewCollectionSchedule(CollectionSchedule{Kind: "daily", Timezone: loc.String(), TimeOfDay: "02:30"}, before, 2)
	if e != nil || len(out) != 2 || out[0].In(loc).Day() != 9 {
		t.Fatal(out, e)
	}
	// Fall fold: return a single occurrence for each local date, not twice.
	before = time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	out, e = PreviewCollectionSchedule(CollectionSchedule{Kind: "daily", Timezone: loc.String(), TimeOfDay: "01:30"}, before, 3)
	if e != nil || len(out) != 3 || out[0].In(loc).Day() != 1 || out[1].In(loc).Day() != 2 {
		t.Fatal(out, e)
	}
}

func TestCollectionPlanSnapshotWorkspaceAndRevision(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "interval")
	if p.Revision != 1 || p.Task.SourceID == "" || p.NextRunAt == nil || p.QueueTTLSeconds != 3600 {
		t.Fatal(p)
	}
	r := triggerPlan(t, s, ctx, p.ID, "first")
	in := planInput("interval")
	in.Task.SourceID = p.Task.SourceID
	in.Task.Goal = "New goal"
	in.Enabled = false
	updated, e := s.UpdateCollectionPlan(ctx, p.ID, p.Revision, in)
	if e != nil || updated.NextRunAt != nil {
		t.Fatal(updated, e)
	}
	if _, e = s.UpdateCollectionPlan(ctx, p.ID, p.Revision, in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	task, e := s.CollectionTask(ctx, r.TaskID)
	if e != nil || strings.Contains(task.Goal, "New goal") {
		t.Fatal(task, e)
	}
	if _, e = s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "paused"}); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	other := User{ID: uuid.NewString(), Email: "other-plan@example.com", Enabled: true, Revision: 1}
	if e = s.Store.Update(context.Background(), func(tx Tx) error { return recordSave(tx, "learning_user", other.ID, userRecord{Profile: other}) }); e != nil {
		t.Fatal(e)
	}
	otherCtx := WithWorkspace(context.Background(), other.ID)
	if _, e = s.CollectionPlan(otherCtx, p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, _, e = s.CollectionPlanRuns(otherCtx, p.ID, 10, 0); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.TriggerCollectionPlan(otherCtx, p.ID, CollectionPlanTrigger{EventID: "steal"}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	deleted, e := s.DeleteCollectionPlan(ctx, p.ID, updated.Revision)
	if e != nil || deleted.DeletedAt == nil {
		t.Fatal(deleted, e)
	}
	list, e := s.CollectionPlans(ctx, false)
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
	list, e = s.CollectionPlans(ctx, true)
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	runs, _, e := s.CollectionPlanRuns(ctx, p.ID, 10, 0)
	if e != nil || len(runs) != 1 {
		t.Fatal(runs, e)
	}
	if _, e = s.CollectionTask(ctx, r.TaskID); e != nil {
		t.Fatal("archiving must not cancel/delete existing tasks", e)
	}
}

func TestCollectionPlanConcurrentDedupeOverlapAndQueueExpiry(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "interval")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "same-event"}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	runs, total, e := s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if e != nil || total != 1 || runs[0].TaskID == "" {
		t.Fatal(runs, total, e)
	}
	r := triggerPlan(t, s, ctx, p.ID, "second")
	if r.DispatchStatus != "skipped" || r.Reason != "overlap" {
		t.Fatal(r)
	}
	_, count, e := s.CollectionTasks(ctx, 100, 0)
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
	now := s.Now().Add(2 * time.Hour)
	s.Now = func() time.Time { return now }
	if _, e = s.SweepCollectionPlans(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	task, e := s.CollectionTask(ctx, runs[0].TaskID)
	if e != nil || task.Status != TaskStatusExpired || task.Result.Error.Code != "queue_expired" {
		t.Fatal(task, e)
	}
	_, count, e = s.CollectionTasks(ctx, 100, 0)
	if e != nil || count != 2 {
		t.Fatal(count, e)
	}
	p, e = s.CollectionPlan(ctx, p.ID)
	if e != nil || !p.NextRunAt.After(now) {
		t.Fatal(p, e)
	}
}

func TestCollectionPlanConcurrentSchedulerCoalesces(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "interval")
	now := s.Now().Add(48*time.Hour + 30*time.Minute)
	s.Now = func() time.Time { return now }
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.SweepCollectionPlans(context.Background(), 100); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	runs, total, e := s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if e != nil || total != 1 || runs[0].DispatchStatus != "queued" {
		t.Fatal(runs, total, e)
	}
	if !runs[0].ScheduledFor.Equal(now.Add(-30 * time.Minute)) {
		t.Fatal(runs[0])
	}
	updated, _ := s.CollectionPlan(ctx, p.ID)
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(now.Add(30*time.Minute)) {
		t.Fatal(updated)
	}
}

func TestCollectionPlanMisfireAndBlockedSource(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	at := s.Now().Add(time.Minute)
	in := planInput("once")
	in.Schedule = CollectionSchedule{Kind: "once", At: &at}
	in.CatchUpSeconds = 60
	p, e := s.CreateCollectionPlan(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	now := s.Now().Add(time.Hour)
	s.Now = func() time.Time { return now }
	if _, e = s.SweepCollectionPlans(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	runs, _, _ := s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if len(runs) != 1 || runs[0].Reason != "misfire_window_exceeded" || runs[0].TaskID != "" {
		t.Fatal(runs)
	}
	p, e = s.CollectionPlan(ctx, p.ID)
	if e != nil || p.NextRunAt != nil {
		t.Fatal(p, e)
	}
	p = createPlan(t, s, ctx, "manual")
	if e = s.Store.Update(ctx, func(tx Tx) error {
		source, e := tx.Source(p.Task.SourceID)
		if e != nil {
			return e
		}
		source.Enabled = false
		return tx.SaveSource(source)
	}); e != nil {
		t.Fatal(e)
	}
	r := triggerPlan(t, s, ctx, p.ID, "disabled-source")
	if r.DispatchStatus != "blocked" || r.Reason != "source_or_agent_unavailable" {
		t.Fatal(r)
	}
}

func TestCollectionWebhookSecurityIdempotencyAndRotation(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	in := planInput("webhook")
	in.Schedule.IntervalMinutes = 0
	in.AllowURL = true
	in.AllowKeyword = true
	p, e := s.CreateCollectionPlan(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	p, key, e := s.RotateCollectionTriggerKey(ctx, p.ID, p.Revision, false)
	if e != nil || !p.TriggerKeySet || !strings.HasPrefix(key, "wztg_") {
		t.Fatal(p, e)
	}
	now := s.Now()
	input := CollectionPlanTrigger{EventID: "news-1", SentAt: &now, URL: "https://go.dev/blog/", Keyword: "Go"}
	r, e := s.TriggerCollectionWebhook(context.Background(), p.ID, key, input)
	if e != nil || r.TaskID == "" {
		t.Fatal(r, e)
	}
	again, e := s.TriggerCollectionWebhook(context.Background(), p.ID, key, input)
	if e != nil || again.ID != r.ID {
		t.Fatal(again, e)
	}
	input.Keyword = "changed"
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, input); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	input.EventID = "news-2"
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, input); !errors.Is(e, ErrTriggerRateLimited) {
		t.Fatal(e)
	}
	input.URL = "https://example.com"
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, input); !errors.Is(e, ErrDomain) {
		t.Fatal(e)
	}
	input.URL = "http://127.0.0.1/"
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, input); !errors.Is(e, ErrDomain) {
		t.Fatal(e)
	}
	input.URL = "https://go.dev/"
	old := now.Add(-6 * time.Minute)
	input.SentAt = &old
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, input); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, "wzag_bad", input); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	list, _ := s.CollectionPlans(ctx, false)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), "trigger_key_hash") {
		t.Fatal("secret leaked", string(raw))
	}
	p, newKey, e := s.RotateCollectionTriggerKey(ctx, p.ID, p.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	input.SentAt = &now
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, input); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	p, _, e = s.RotateCollectionTriggerKey(ctx, p.ID, p.Revision, true)
	if e != nil || p.TriggerKeySet {
		t.Fatal(p, e)
	}
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, newKey, input); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
}

type collectionFailStore struct{ Store }
type collectionFailTx struct{ Tx }

func (t collectionFailTx) SaveRecord(kind, id string, raw json.RawMessage) error {
	if kind == collectionRunKind {
		return errors.New("injected run persistence failure")
	}
	return t.Tx.SaveRecord(kind, id, raw)
}
func (st collectionFailStore) Update(ctx context.Context, fn func(Tx) error) error {
	return st.Store.Update(ctx, func(tx Tx) error { return fn(collectionFailTx{tx}) })
}
func TestCollectionDispatchRollback(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "interval")
	original := s.Store
	s.Store = collectionFailStore{original}
	if _, e := s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "rollback"}); e == nil {
		t.Fatal("expected failure")
	}
	s.Store = original
	_, count, e := s.CollectionTasks(ctx, 100, 0)
	if e != nil || count != 0 {
		t.Fatal(count, e)
	}
	got, e := s.CollectionPlan(ctx, p.ID)
	if e != nil || got.LastRunAt != nil || !got.NextRunAt.Equal(*p.NextRunAt) {
		t.Fatal(got, e)
	}
}

func TestCollectionRunSurvivesTaskDeletionAndNoReset(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "manual")
	r := triggerPlan(t, s, ctx, p.ID, "retain")
	if _, e := s.CancelAgentTask(ctx, r.TaskID, "cancelled"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RetryAgentTask(ctx, r.TaskID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.DeleteCollectionTask(ctx, r.TaskID); e != nil {
		t.Fatal(e)
	}
	runs, total, e := s.CollectionPlanRuns(ctx, p.ID, 10, 0)
	if e != nil || total != 1 || runs[0].TaskStatus != TaskStatusCancelled || runs[0].FinishedAt == nil {
		t.Fatal(runs, e)
	}
}

func TestCollectionPlansPersistAcrossRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plans.json")
	store, e := OpenFileStore(file)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(store)
	base, ctx, owner := agentFixture(t)
	if e = s.Store.Update(context.Background(), func(tx Tx) error {
		return base.Store.View(context.Background(), func(src Tx) error {
			user, e := recordAs[userRecord](src, "learning_user", owner)
			if e != nil {
				return e
			}
			return recordSave(tx, "learning_user", owner, user)
		})
	}); e != nil {
		t.Fatal(e)
	}
	s.Now = base.Now
	p := createPlan(t, s, ctx, "interval")
	store.Close()
	reopened, e := OpenFileStore(file)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	s = NewService(reopened)
	now := base.Now().Add(2 * time.Hour)
	s.Now = func() time.Time { return now }
	if _, e = s.SweepCollectionPlans(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	runs, total, e := s.CollectionPlanRuns(ctx, p.ID, 10, 0)
	if e != nil || total != 1 || runs[0].TaskID == "" {
		t.Fatal(runs, e)
	}
}
