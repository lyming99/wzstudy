package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func unreadCollectionBody(operator string, threshold, minutes int) map[string]any {
	body := collectionBody("unread_inventory")
	body["schedule"] = map[string]any{"kind": "unread_inventory", "timezone": "UTC", "interval_minutes": minutes,
		"unread_condition": map[string]any{"operator": operator, "threshold": threshold}}
	return body
}
func seedHTTPUnreadItems(t *testing.T, s *study.Service, owner string, count int) {
	t.Helper()
	ctx := study.WithWorkspace(context.Background(), owner)
	source, err := s.EnsureAgentCollectionSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Update(ctx, func(tx study.Tx) error {
		for i := 0; i < count; i++ {
			id := uuid.NewString()
			content := study.Content{Kind: "article", Title: "未读条件测试", ExternalID: id, Content: "只用于隔离接口回归"}
			item := study.Item{ID: id, OwnerID: owner, SourceID: source.ID, ExternalID: id, Revision: 1, Status: "published", Draft: content, CreatedAt: s.Now(), UpdatedAt: s.Now(),
				Published: &study.PublicItem{ID: id, SourceID: source.ID, Revision: 1, Content: content, UpdatedAt: s.Now()}}
			if e := tx.SaveItem(item); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionUnreadPreviewAndManualConditionAreWorkspaceScoped(t *testing.T) {
	s, h := collectionApp(t)
	alice, aliceToken := managedLearner(t, h, "unread-alice")
	bob, bobToken := managedLearner(t, h, "unread-bob")
	seedHTTPUnreadItems(t, s, alice.ID, 2)
	seedHTTPUnreadItems(t, s, bob.ID, 5)
	base := "/api/v1/workspace/agent/collection-plans"
	body := unreadCollectionBody("lt", 3, 10)
	var p study.CollectionPlan
	data(t, request(t, h, "POST", base, aliceToken, body), &p)
	if p.NextRunAt == nil || p.LastUnreadCheck != nil || p.Schedule.UnreadCondition == nil || *p.Schedule.UnreadCondition.Threshold != 3 {
		t.Fatal(p)
	}
	for _, tc := range []struct {
		base, token string
		want        int
		matched     bool
	}{
		{base, aliceToken, 2, true}, {base, bobToken, 5, false}, {"/api/v1/admin/agent/collection-plans", adminKey, 0, true},
	} {
		var preview study.CollectionPlanPreview
		data(t, request(t, h, "POST", tc.base+"/preview", tc.token, map[string]any{"schedule": body["schedule"]}), &preview)
		if len(preview.Times) != 5 || preview.UnreadCheck == nil || preview.UnreadCheck.UnreadCount != tc.want || preview.UnreadCheck.Matched != tc.matched {
			t.Fatal(tc.want, preview)
		}
		if preview.Times[0].Before(s.Now().Add(9 * time.Minute)) {
			t.Fatal(preview.Times)
		}
	}
	for _, owner := range []string{alice.ID, bob.ID} {
		_, count, err := s.CollectionTasks(study.WithWorkspace(context.Background(), owner), 100, 0)
		if err != nil || count != 0 {
			t.Fatal(count, err)
		}
	}
	if w := request(t, h, "POST", base+"/preview", "", map[string]any{"schedule": body["schedule"]}); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, h, "GET", base+"/"+p.ID, bobToken, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	var run study.CollectionPlanRun
	data(t, request(t, h, "POST", base+"/"+p.ID+"/run", aliceToken, map[string]any{"event_id": "unread-manual"}), &run)
	if run.DispatchStatus != "queued" || run.TaskID == "" || run.UnreadCheck == nil || run.UnreadCheck.UnreadCount != 2 {
		t.Fatal(run)
	}
	var other study.CollectionPlan
	data(t, request(t, h, "POST", base, bobToken, body), &other)
	run = study.CollectionPlanRun{}
	data(t, request(t, h, "POST", base+"/"+other.ID+"/run", bobToken, map[string]any{"event_id": "unread-not-met"}), &run)
	if run.DispatchStatus != "skipped" || run.TaskID != "" || run.Reason != "unread_condition_not_met" || run.UnreadCheck.UnreadCount != 5 {
		t.Fatal(run)
	}
}

func TestCollectionUnreadStrictValidationAndReadOnlyPreview(t *testing.T) {
	_, h := collectionApp(t)
	_, token := managedLearner(t, h, "unread-validation")
	base := "/api/v1/workspace/agent/collection-plans"
	var beforeSources []study.Source
	data(t, request(t, h, "GET", "/api/v1/workspace/sources", token, nil), &beforeSources)
	for _, body := range []map[string]any{unreadCollectionBody("invalid", 3, 10), unreadCollectionBody("lt", -1, 10), unreadCollectionBody("lt", 1000001, 10), unreadCollectionBody("lt", 3, 0), unreadCollectionBody("lt", 3, 10081)} {
		for _, path := range []string{base, base + "/preview"} {
			in := any(body)
			if path != base {
				in = map[string]any{"schedule": body["schedule"]}
			}
			if w := request(t, h, "POST", path, token, in); w.Code != 400 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, condition := range []map[string]any{{"operator": "lt"}, {"operator": "lt", "threshold": 1, "owner_id": "foreign"}, {"operator": "lt", "threshold": 1.5}} {
		in := map[string]any{"schedule": map[string]any{"kind": "unread_inventory", "timezone": "UTC", "interval_minutes": 10, "unread_condition": condition}}
		if w := request(t, h, "POST", base+"/preview", token, in); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var preview study.CollectionPlanPreview
	zero := unreadCollectionBody("eq", 0, 1)
	data(t, request(t, h, "POST", base+"/preview", token, map[string]any{"schedule": zero["schedule"]}), &preview)
	if preview.UnreadCheck == nil || preview.UnreadCheck.Threshold != 0 || preview.UnreadCheck.UnreadCount != 0 || !preview.UnreadCheck.Matched {
		t.Fatal(preview)
	}
	var plans []study.CollectionPlan
	data(t, request(t, h, "GET", base, token, nil), &plans)
	if len(plans) != 0 {
		t.Fatal("preview created plans", plans)
	}
	var sources []study.Source
	data(t, request(t, h, "GET", "/api/v1/workspace/sources", token, nil), &sources)
	if len(sources) != len(beforeSources) {
		t.Fatal("preview changed sources", beforeSources, sources)
	}
}
