package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestRetiredPushRoutesAndUIAreRemoved(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	client, key := managedClient(t, h, "learning-client", []string{"read", "sync"})
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/admin/plans"},
		{http.MethodPost, "/api/v1/admin/plans/old-plan/action"},
		{http.MethodGet, "/api/v1/clients/" + client.ID + "/inbox"},
		{http.MethodGet, "/api/v1/stream"},
	} {
		if out := request(t, h, tc.method, tc.path, adminKey, map[string]any{}); out.Code != http.StatusNotFound {
			t.Fatalf("retired endpoint remains available: %s %s: %d", tc.method, tc.path, out.Code)
		}
	}
	for _, path := range []string{"/api/v1/stream", "/api/v1/clients/" + client.ID + "/inbox"} {
		if out := request(t, h, http.MethodGet, path, key, nil); out.Code != http.StatusNotFound {
			t.Fatalf("client can still reach %s: %d", path, out.Code)
		}
	}
	workspace := request(t, h, http.MethodGet, "/api/v1/admin/workspace", adminKey, nil)
	if workspace.Code != http.StatusOK || strings.Contains(workspace.Body.String(), `"plans"`) {
		t.Fatal("workspace still depends on push plans", workspace.Body.String())
	}
	for _, path := range []string{"/admin/", "/admin/app.js"} {
		out := request(t, h, http.MethodGet, path, "", nil)
		if out.Code != http.StatusOK {
			t.Fatal(path, out.Code)
		}
		for _, retired := range []string{`data-route="push"`, "renderPush", "state.plans", "plan-form", "bulk-push", "接收推送", "/api/v1/stream"} {
			if strings.Contains(out.Body.String(), retired) {
				t.Fatalf("%s exposes retired UI %q", path, retired)
			}
		}
	}
	for _, permissions := range [][]string{{"inbox"}, {"read", "inbox"}} {
		if out := request(t, h, http.MethodPost, "/api/v1/admin/sources", adminKey, map[string]any{"slug": "retired", "name": "retired", "kind": "manual", "permissions": permissions}); out.Code != http.StatusBadRequest {
			t.Fatal("retired permission accepted", out.Code)
		}
	}
}

func TestClientQueriesAndPollingUseOnlyPublishedSnapshots(t *testing.T) {
	s, h, _ := managementApp(t, 1<<20)
	_, key := managedClient(t, h, "learning-client", []string{"read", "sync"})
	ctx := context.Background()
	item, err := s.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Kind: "article", Title: "Learning Go", Content: "approved lesson", Category: "编程", Tags: []string{"Go"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.Publication(ctx, item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []study.PublicItem `json:"items"`
	}
	data(t, request(t, h, "GET", "/api/v1/items?kind=article&category=%E7%BC%96%E7%A8%8B&tag=Go&q=learning&limit=1&offset=0", key, nil), &page)
	if len(page.Items) != 1 || page.Items[0].ID != item.ID {
		t.Fatal("client-side recommendation candidates cannot be queried", page)
	}
	var initial study.ChangePage
	data(t, request(t, h, "GET", "/api/v1/changes?after=0", key, nil), &initial)
	if len(initial.Changes) != 1 || initial.Changes[0].Item.Title != "Learning Go" {
		t.Fatal(initial)
	}
	item, err = s.SaveManaged(ctx, item.ID, study.ManagedInput{Revision: item.Revision, Status: "pending", Content: study.Content{Kind: "article", Title: "PRIVATE draft", Content: "must not leak", Category: "编程", Tags: []string{"Go"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var detail study.PublicItem
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID, key, nil), &detail)
	if detail.Title != "Learning Go" || detail.Content.Content != "approved lesson" {
		t.Fatal("unreviewed edit leaked", detail)
	}
	var unchanged study.ChangePage
	data(t, request(t, h, "GET", "/api/v1/changes?after=1", key, nil), &unchanged)
	if len(unchanged.Changes) != 0 || unchanged.NextCursor != initial.NextCursor {
		t.Fatal("draft edit generated a public event", unchanged)
	}
	item, err = s.Publication(ctx, item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	var updated study.ChangePage
	data(t, request(t, h, "GET", "/api/v1/changes?after=1", key, nil), &updated)
	if len(updated.Changes) != 1 || updated.Changes[0].Item.Title != "PRIVATE draft" || updated.HasMore {
		t.Fatal("published edit cannot be pulled", updated)
	}
	if _, err = s.Publication(ctx, item.ID, item.Revision, false); err != nil {
		t.Fatal(err)
	}
	var removed study.ChangePage
	data(t, request(t, h, "GET", "/api/v1/changes?after=2", key, nil), &removed)
	if len(removed.Changes) != 1 || removed.Changes[0].Operation != "delete" || removed.Changes[0].Item != nil {
		t.Fatal("client cannot remove stale recommendation candidates", removed)
	}
	if out := request(t, h, "GET", "/api/v1/items/"+item.ID, key, nil); out.Code != 404 {
		t.Fatal("unpublished material still queryable", out.Code)
	}
}
