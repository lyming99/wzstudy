package study

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func unreadSchedule(operator string, threshold, minutes int) CollectionSchedule {
	return CollectionSchedule{Kind: "unread_inventory", Timezone: "UTC", IntervalMinutes: minutes,
		UnreadCondition: &CollectionUnreadCondition{Operator: operator, Threshold: &threshold}}
}
func unreadPlan(t *testing.T, s *Service, ctx context.Context, operator string, threshold int) CollectionPlan {
	t.Helper()
	in := planInput("unread_inventory")
	in.Schedule = unreadSchedule(operator, threshold, 10)
	p, err := s.CreateCollectionPlan(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func unreadItems(t *testing.T, s *Service, ctx context.Context, n int, public bool) []Item {
	t.Helper()
	source, err := s.EnsureAgentCollectionSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]Item, n)
	err = s.Store.Update(ctx, func(tx Tx) error {
		for i := range items {
			id := uuid.NewString()
			content := Content{Kind: "article", ExternalID: id, Title: "未读库存测试", Content: "隔离测试正文"}
			item := Item{ID: id, OwnerID: WorkspaceUserID(ctx), SourceID: source.ID, ExternalID: id,
				Revision: 1, Status: "draft", Draft: content, CreatedAt: s.Now(), UpdatedAt: s.Now()}
			if public {
				item.Published = &PublicItem{ID: id, SourceID: source.ID, Revision: 1, Content: content, UpdatedAt: s.Now()}
				item.Status = "published"
			}
			if err := tx.SaveItem(item); err != nil {
				return err
			}
			items[i] = item
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestCollectionUnreadConditionValidationAndBoundaries(t *testing.T) {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, op := range []string{"lt", "lte", "eq", "gte", "gt"} {
		schedule := unreadSchedule(op, 20, 10)
		out, err := PreviewCollectionSchedule(schedule, ref, 5)
		if err != nil || len(out) != 5 || !out[0].Equal(ref.Add(10*time.Minute)) {
			t.Fatal(out, err)
		}
		for _, count := range []int{0, 19, 20, 21} {
			want := map[string]bool{"lt": count < 20, "lte": count <= 20, "eq": count == 20, "gte": count >= 20, "gt": count > 20}[op]
			if got := collectionUnreadMatches(count, *schedule.UnreadCondition); got != want {
				t.Fatal(op, count, got, want)
			}
		}
	}
	if !collectionUnreadMatches(0, *unreadSchedule("eq", 0, 1).UnreadCondition) {
		t.Fatal("zero must be explicit and valid")
	}
	invalid := []CollectionSchedule{unreadSchedule("bad", 1, 10), unreadSchedule("lt", -1, 10), unreadSchedule("lt", 1000001, 10), unreadSchedule("lt", 1, 0), unreadSchedule("lt", 1, 10081), {Kind: "unread_inventory", IntervalMinutes: 10}}
	missing := unreadSchedule("lt", 1, 10)
	missing.UnreadCondition.Threshold = nil
	invalid = append(invalid, missing)
	wrong := unreadSchedule("lt", 1, 10)
	wrong.Kind = "interval"
	invalid = append(invalid, wrong)
	wrong = unreadSchedule("lt", 1, 10)
	wrong.TimeOfDay = "09:00"
	invalid = append(invalid, wrong)
	wrong = unreadSchedule("lt", 1, 10)
	at := ref.Add(time.Hour)
	wrong.At = &at
	invalid = append(invalid, wrong)
	for _, in := range invalid {
		if _, err := PreviewCollectionSchedule(in, ref, 5); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", in, err)
		}
	}
}

func TestCollectionUnreadUsesPublicProjectionAndOwnerReadingIdentity(t *testing.T) {
	s, ctx, owner := agentFixture(t)
	items := unreadItems(t, s, ctx, 8, true)
	unreadItems(t, s, ctx, 4, false)
	err := s.Store.Update(ctx, func(tx Tx) error {
		states := []LearningState{
			{ClientID: owner, ItemID: items[1].ID, ViewCount: 1},
			{ClientID: owner, ItemID: items[2].ID, Progress: map[string]LearningProgress{"article": {Revision: 1}}},
			{ClientID: uuid.NewString(), ItemID: items[5].ID, ViewCount: 1},
		}
		for _, state := range states {
			if err := recordSave(tx, "learning_state", state.ItemID, state); err != nil {
				return err
			}
		}
		for i, active := range map[int]bool{3: true, 4: false} {
			state := FeedbackState{ClientID: owner, ItemID: items[i].ID, Actions: map[string]FeedbackActionState{"read": {Active: active}}}
			if err := recordSave(tx, "feedback_state", state.ItemID, state); err != nil {
				return err
			}
		}
		// A newer private revision retains its old public projection. It also
		// does not erase a prior view or turn a read item into unread inventory.
		for _, i := range []int{1, 6} {
			item := items[i]
			item.Revision = 2
			item.Status = "draft"
			item.Draft.Title = "新草稿"
			if err := tx.SaveItem(item); err != nil {
				return err
			}
		}
		deleted := items[7]
		now := s.Now()
		deleted.DeletedAt = &now
		return tx.SaveItem(deleted)
	})
	if err != nil {
		t.Fatal(err)
	}
	other := User{ID: uuid.NewString(), Email: "unread-other@example.com", Enabled: true, Revision: 1}
	if err = s.Store.Update(context.Background(), func(tx Tx) error { return recordSave(tx, "learning_user", other.ID, userRecord{Profile: other}) }); err != nil {
		t.Fatal(err)
	}
	otherCtx := WithWorkspace(context.Background(), other.ID)
	unreadItems(t, s, otherCtx, 7, true)
	// Even a colliding personal-reading identity in another workspace is hidden.
	if err = s.Store.Update(otherCtx, func(tx Tx) error {
		return recordSave(tx, "learning_state", "foreign", LearningState{ClientID: owner, ItemID: items[0].ID, ViewCount: 9})
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewCollectionPlan(ctx, unreadSchedule("lt", 5, 10), 5)
	if err != nil || preview.UnreadCheck == nil || preview.UnreadCheck.UnreadCount != 4 || !preview.UnreadCheck.Matched {
		t.Fatal(preview, err)
	}
	preview, err = s.PreviewCollectionPlan(otherCtx, unreadSchedule("eq", 7, 10), 5)
	if err != nil || preview.UnreadCheck.UnreadCount != 7 || !preview.UnreadCheck.Matched {
		t.Fatal(preview, err)
	}
	if _, err = s.PreviewCollectionPlan(context.Background(), unreadSchedule("lt", 5, 10), 5); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestCollectionUnreadCountsBeyondOnePageAndPreviewIsReadOnly(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	unreadItems(t, s, ctx, 501, true)
	preview, err := s.PreviewCollectionPlan(ctx, unreadSchedule("gte", 501, 10), 5)
	if err != nil || preview.UnreadCheck.UnreadCount != 501 || !preview.UnreadCheck.Matched {
		t.Fatal(preview, err)
	}
	plans, err := s.CollectionPlans(ctx, true)
	if err != nil || len(plans) != 0 {
		t.Fatal(plans, err)
	}
	_, count, err := s.CollectionTasks(ctx, 100, 0)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestCollectionUnreadFalsePollAdvancesAndManualRechecksIdempotently(t *testing.T) {
	s, ctx, owner := agentFixture(t)
	items := unreadItems(t, s, ctx, 3, true)
	p := unreadPlan(t, s, ctx, "lt", 2)
	now := s.Now().Add(10 * time.Minute)
	s.Now = func() time.Time { return now }
	if n, err := s.SweepCollectionPlans(context.Background(), 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	p, err := s.CollectionPlan(ctx, p.ID)
	if err != nil || p.LastUnreadCheck == nil || p.LastUnreadCheck.UnreadCount != 3 || p.LastUnreadCheck.Matched || p.LastRunAt != nil || !p.NextRunAt.After(now) || p.Revision != 1 {
		t.Fatal(p, err)
	}
	_, total, err := s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if err != nil || total != 0 {
		t.Fatal(total, err)
	}
	r, err := s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "manual-check"})
	if err != nil || r.DispatchStatus != "skipped" || r.Reason != "unread_condition_not_met" || r.TaskID != "" || r.UnreadCheck.UnreadCount != 3 {
		t.Fatal(r, err)
	}
	if err = s.Store.Update(ctx, func(tx Tx) error {
		for _, item := range items {
			if e := recordSave(tx, "learning_state", item.ID, LearningState{ClientID: owner, ItemID: item.ID, ViewCount: 1}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "manual-check"})
	if err != nil || again.ID != r.ID || again.DispatchStatus != "skipped" || again.UnreadCheck.UnreadCount != 3 {
		t.Fatal(again, err)
	}
	r, err = s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "manual-check-new"})
	if err != nil || r.TaskID == "" || r.UnreadCheck.UnreadCount != 0 || !r.UnreadCheck.Matched {
		t.Fatal(r, err)
	}
	got, err := s.CollectionPlan(ctx, p.ID)
	if err != nil || !got.NextRunAt.Equal(*p.NextRunAt) {
		t.Fatal(got, err)
	}
}

func TestCollectionUnreadConcurrentScheduleOverlapAndCoalescing(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := unreadPlan(t, s, ctx, "lt", 2)
	// Long outage coalesces to one current check, not a backlog of old polls.
	now := s.Now().Add(48*time.Hour + 27*time.Minute)
	s.Now = func() time.Time { return now }
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SweepCollectionPlans(context.Background(), 100); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	runs, total, err := s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if err != nil || total != 1 || runs[0].TaskID == "" || runs[0].TriggerSource != "condition" || runs[0].UnreadCheck == nil || !runs[0].UnreadCheck.Matched {
		t.Fatal(runs, total, err)
	}
	task, err := s.CollectionTask(ctx, runs[0].TaskID)
	if err != nil || !strings.Contains(task.Goal, now.Format(time.RFC3339)) {
		t.Fatal(task, err)
	}
	p, err = s.CollectionPlan(ctx, p.ID)
	if err != nil || !p.NextRunAt.After(now) {
		t.Fatal(p, err)
	}
	now = *p.NextRunAt
	if _, err = s.SweepCollectionPlans(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	runs, total, err = s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if err != nil || total != 2 || runs[0].Reason != "overlap" {
		t.Fatal(runs, total, err)
	}
	_, count, err := s.CollectionTasks(ctx, 100, 0)
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = s.CancelAgentTask(ctx, task.ID, "test complete"); err != nil {
		t.Fatal(err)
	}
	p, _ = s.CollectionPlan(ctx, p.ID)
	now = *p.NextRunAt
	if _, err = s.SweepCollectionPlans(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	_, count, err = s.CollectionTasks(ctx, 100, 0)
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	// Once replenished, the next poll observes the new stock without a fake run.
	unreadItems(t, s, ctx, 3, true)
	p, _ = s.CollectionPlan(ctx, p.ID)
	now = *p.NextRunAt
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	p, err = s.CollectionPlan(ctx, p.ID)
	if err != nil || p.LastUnreadCheck.Matched || p.LastUnreadCheck.UnreadCount != 3 {
		t.Fatal(p, err)
	}
	_, total, err = s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if err != nil || total != 3 {
		t.Fatal(total, err)
	}
	in := planInput("unread_inventory")
	in.Schedule = unreadSchedule("lt", 2, 10)
	in.Name = "Renamed"
	p, err = s.UpdateCollectionPlan(ctx, p.ID, p.Revision, in)
	if err != nil || p.LastUnreadCheck == nil {
		t.Fatal(p, err)
	}
	in.Schedule = unreadSchedule("lte", 4, 10)
	p, err = s.UpdateCollectionPlan(ctx, p.ID, p.Revision, in)
	if err != nil || p.LastUnreadCheck != nil {
		t.Fatal(p, err)
	}
	p, err = s.SetCollectionPlanEnabled(ctx, p.ID, p.Revision, false)
	if err != nil || p.NextRunAt != nil {
		t.Fatal(p, err)
	}
	p, err = s.SetCollectionPlanEnabled(ctx, p.ID, p.Revision, true)
	if err != nil || !p.NextRunAt.Equal(now.Add(10*time.Minute)) {
		t.Fatal(p, err)
	}
}

func TestCollectionUnreadFileRestartRetainsCheckCursor(t *testing.T) {
	file := filepath.Join(t.TempDir(), "unread.json")
	store, err := OpenFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := NewService(store)
	base, ctx, owner := agentFixture(t)
	if err = s.Store.Update(context.Background(), func(tx Tx) error {
		return base.Store.View(context.Background(), func(from Tx) error {
			user, e := recordAs[userRecord](from, "learning_user", owner)
			if e != nil {
				return e
			}
			return recordSave(tx, "learning_user", owner, user)
		})
	}); err != nil {
		t.Fatal(err)
	}
	now := base.Now()
	s.Now = func() time.Time { return now }
	items := unreadItems(t, s, ctx, 1, true)
	p := unreadPlan(t, s, ctx, "lt", 1)
	now = *p.NextRunAt
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	store.Close()
	reopened, err := OpenFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = NewService(reopened)
	s.Now = func() time.Time { return now }
	p, err = s.CollectionPlan(ctx, p.ID)
	if err != nil || p.LastUnreadCheck == nil || p.LastUnreadCheck.Matched || p.LastUnreadCheck.UnreadCount != 1 || !p.NextRunAt.After(now) {
		t.Fatal(p, err)
	}
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if err = s.Store.Update(ctx, func(tx Tx) error {
		return recordSave(tx, "learning_state", items[0].ID, LearningState{ClientID: owner, ItemID: items[0].ID, ViewCount: 1})
	}); err != nil {
		t.Fatal(err)
	}
	now = *p.NextRunAt
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	reopened.Close()
	again, err := OpenFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	s = NewService(again)
	s.Now = func() time.Time { return now }
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	runs, total, err := s.CollectionPlanRuns(ctx, p.ID, 100, 0)
	if err != nil || total != 1 || runs[0].UnreadCheck == nil || runs[0].UnreadCheck.UnreadCount != 0 || runs[0].TaskID == "" {
		t.Fatal(runs, total, err)
	}
}

func TestCollectionUnreadDispatchRollbackIncludesObservation(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := unreadPlan(t, s, ctx, "lt", 2)
	original := s.Store
	s.Store = collectionFailStore{original}
	_, err := s.TriggerCollectionPlan(ctx, p.ID, CollectionPlanTrigger{EventID: "rollback-unread"})
	if err == nil || !strings.Contains(err.Error(), "injected run persistence failure") {
		t.Fatal(err)
	}
	s.Store = original
	_, count, err := s.CollectionTasks(ctx, 100, 0)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	got, err := s.CollectionPlan(ctx, p.ID)
	if err != nil || got.LastUnreadCheck != nil || got.LastRunAt != nil || !got.NextRunAt.Equal(*p.NextRunAt) {
		t.Fatal(got, err)
	}
}
