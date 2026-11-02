package study

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type noPlanScanStore struct{ Store }
type noPlanScanTx struct{ Tx }

func (tx noPlanScanTx) Records(kind string) ([]json.RawMessage, error) {
	if kind == collectionPlanKind {
		return nil, errors.New("plan scan forbidden during authentication")
	}
	return tx.Tx.Records(kind)
}
func (st noPlanScanStore) View(ctx context.Context, fn func(Tx) error) error {
	return st.Store.View(ctx, func(tx Tx) error { return fn(noPlanScanTx{tx}) })
}
func (st noPlanScanStore) Update(ctx context.Context, fn func(Tx) error) error {
	return st.Store.Update(ctx, func(tx Tx) error { return fn(noPlanScanTx{tx}) })
}

func TestCollectionWebhookAuthenticationUsesKeyedLookup(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "webhook")
	p, key, e := s.RotateCollectionTriggerKey(ctx, p.ID, p.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	s.Store = noPlanScanStore{s.Store}
	now := s.Now()
	input := CollectionPlanTrigger{EventID: "keyed", SentAt: &now}
	if _, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key+"x", input); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	r, e := s.TriggerCollectionWebhook(context.Background(), p.ID, key, input)
	if e != nil || r.TaskID == "" {
		t.Fatal(r, e)
	}
}
func TestCollectionExpiredQueueCannotBeClaimedAndPauseBrokenBinding(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	p := createPlan(t, s, ctx, "manual")
	r := triggerPlan(t, s, ctx, p.ID, "ttl")
	agent, _ := agentIdentity(t, s, ctx)
	now := s.Now().Add(2 * time.Hour)
	s.Now = func() time.Time { return now }
	if task, ok, e := s.ClaimAgentTask(ctx, agent.ID, 1); e != nil || ok {
		t.Fatal(task, ok, e)
	}
	task, e := s.CollectionTask(ctx, r.TaskID)
	if e != nil || task.Status != TaskStatusExpired {
		t.Fatal(task, e)
	}
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
	p, e = s.SetCollectionPlanEnabled(ctx, p.ID, p.Revision, false)
	if e != nil || p.Enabled {
		t.Fatal(p, e)
	}
	if _, e = s.SetCollectionPlanEnabled(ctx, p.ID, p.Revision, true); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
}
func TestCollectionDisabledOwnerNeverDispatches(t *testing.T) {
	s, ctx, owner := agentFixture(t)
	p := createPlan(t, s, ctx, "interval")
	hook := createPlan(t, s, ctx, "webhook")
	_, key, e := s.RotateCollectionTriggerKey(ctx, hook.ID, hook.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Store.Update(context.Background(), func(tx Tx) error {
		rec, e := recordAs[userRecord](tx, "learning_user", owner)
		if e != nil {
			return e
		}
		rec.Profile.Enabled = false
		return recordSave(tx, "learning_user", owner, rec)
	}); e != nil {
		t.Fatal(e)
	}
	now := s.Now().Add(2 * time.Hour)
	s.Now = func() time.Time { return now }
	if n, e := s.SweepCollectionPlans(context.Background(), 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if _, e = s.TriggerCollectionWebhook(context.Background(), hook.ID, key, CollectionPlanTrigger{EventID: "disabled", SentAt: &now}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	if e = s.Store.View(context.Background(), func(tx Tx) error {
		runs, e := recordsAs[CollectionPlanRun](tx, collectionRunKind)
		if e != nil {
			return e
		}
		for _, run := range runs {
			if run.PlanID == p.ID || run.PlanID == hook.ID {
				t.Fatal("disabled owner dispatched", run)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestCollectionGoalReservesDispatchContextAndChecksAggregatePayload(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	in := planInput("manual")
	in.Schedule.IntervalMinutes = 0
	in.Task.Goal = strings.Repeat("a", maximumTaskGoalBytes)
	if _, e := s.CreateCollectionPlan(ctx, in); !errors.Is(e, ErrInvalid) {
		t.Fatal("missing room for dispatch context", e)
	}
	in = planInput("webhook")
	in.Schedule.IntervalMinutes = 0
	in.AllowURL = true
	in.Task.Goal = strings.Repeat("a", 3500)
	p, e := s.CreateCollectionPlan(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	_, key, e := s.RotateCollectionTriggerKey(ctx, p.ID, p.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	now := s.Now()
	_, e = s.TriggerCollectionWebhook(context.Background(), p.ID, key, CollectionPlanTrigger{EventID: "too-long", SentAt: &now, URL: "https://go.dev/" + strings.Repeat("a", 1400)})
	if !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	runs, total, e := s.CollectionPlanRuns(ctx, p.ID, 10, 0)
	if e != nil || total != 0 {
		t.Fatal(runs, total, e)
	}
}
