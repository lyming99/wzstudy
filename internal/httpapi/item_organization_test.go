package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestOrganizationHTTPAndImmediateCollectionLifecycle(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := testWorkspaceContext()
	item, e := service.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "未分类资料", Content: "已公开正文"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	item, e = service.Publication(ctx, item.ID, item.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	var collection study.CatalogCollection
	data(t, request(t, h, "POST", "/api/v1/workspace/catalog/collections", workspaceKey, map[string]any{"title": "直接可用合集", "chapters": []any{}}), &collection)
	_, clientKey := managedClient(t, h, "organization-client", nil)
	var page study.CatalogPublicPage
	data(t, request(t, h, "GET", "/api/v1/catalog/collections", clientKey, nil), &page)
	if page.Total != 1 || page.Items[0].ID != collection.ID {
		t.Fatal("new collection requires publish", page)
	}
	for _, action := range []string{"publish", "unpublish"} {
		w := request(t, h, "POST", "/api/v1/workspace/catalog/collections/"+collection.ID+"/"+action, workspaceKey, map[string]int64{"revision": collection.Revision})
		if w.Code != http.StatusGone {
			t.Fatal(action, w.Code, w.Body.String())
		}
	}
	move := map[string]any{"items": []any{map[string]any{"id": item.ID, "revision": item.Revision}}, "collection_id": collection.ID, "collection_revision": collection.Revision}
	for _, key := range []string{"", clientKey, adminKey} {
		if w := request(t, h, "POST", "/api/v1/workspace/items/organize", key, move); w.Code != http.StatusUnauthorized {
			t.Fatal("non-user organized materials", w.Code)
		}
	}
	var result study.ItemOrganizationResult
	data(t, request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, move), &result)
	if result.Moved != 1 || result.CollectionsUpdated != 1 {
		t.Fatal(result)
	}
	var public study.PublicCatalogCollection
	data(t, request(t, h, "GET", "/api/v1/catalog/collections/"+collection.ID, clientKey, nil), &public)
	if len(public.Chapters) != 1 || public.Chapters[0].ItemID != item.ID {
		t.Fatal("move needs additional publish", public)
	}
	var news study.LearningModePage
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news", clientKey, nil), &news)
	if news.Total != 1 {
		t.Fatal("unclassified news not queryable", news)
	}
	classify := map[string]any{"items": []any{map[string]any{"id": item.ID, "revision": item.Revision}}, "category": "人工智能"}
	data(t, request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, classify), &result)
	if result.CategoryUpdated != 1 {
		t.Fatal(result)
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news&category=old-picker", clientKey, nil), &news)
	if news.Total != 1 || news.Items[0].Category != "人工智能" {
		t.Fatal("topic category changed news-mode eligibility", news)
	}
	classify["items"] = []any{map[string]any{"id": item.ID, "revision": result.Items[0].Revision}}
	classify["category"] = ""
	data(t, request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, classify), &result)
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news", clientKey, nil), &news)
	if news.Total != 1 || news.Items[0].Category != "" {
		t.Fatal("clear classification did not restore news", news)
	}
	if w := request(t, h, "POST", "/api/v1/workspace/items/organize?client_id=spoof", workspaceKey, classify); w.Code != 400 {
		t.Fatal("tenant selector accepted", w.Code)
	}
	classify["owner_id"] = "spoof"
	if w := request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, classify); w.Code != 400 {
		t.Fatal("unknown owner accepted", w.Code)
	}
	script := request(t, h, "GET", "/workspace/app.js", "", nil).Body.String()
	for _, action := range []string{"item-move-collection", "item-set-category", "bulk-move-collection", "bulk-set-category", "query-move-collection", "query-set-category"} {
		if !strings.Contains(script, action) {
			t.Fatal("missing organization action", action)
		}
	}
	start := strings.Index(script, "async function loadCatalog()")
	if start < 0 {
		t.Fatal("catalog UI missing")
	}
	end := strings.Index(script[start:], "function catalogOptions")
	if start < 0 || end < 0 {
		t.Fatal("catalog UI missing")
	}
	catalogUI := script[start : start+end]
	if strings.Contains(catalogUI, "catalog-publish") || strings.Contains(catalogUI, "catalog-unpublish") || strings.Contains(catalogUI, "catalog-status") {
		t.Fatal("collection still exposes publish lifecycle")
	}
}

func TestOrganizationHTTPForeignAndStaleBatchesDoNotPartiallyCommit(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	own, e := service.SaveManaged(testWorkspaceContext(), "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "own", Content: "body"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	other, _ := managedLearner(t, h, "organization-other")
	foreign, e := service.SaveManaged(study.WithWorkspace(t.Context(), other.ID), "", study.ManagedInput{Status: "draft", Content: study.Content{Title: "foreign", Content: "private"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	payload := map[string]any{"items": []any{map[string]any{"id": own.ID, "revision": own.Revision}, map[string]any{"id": foreign.ID, "revision": foreign.Revision}}, "category": "must rollback"}
	if w := request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, payload); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	payload["items"] = []any{map[string]any{"id": own.ID, "revision": own.Revision + 1}}
	if w := request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, payload); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	var after study.Item
	data(t, request(t, h, "GET", "/api/v1/workspace/items/"+own.ID, workspaceKey, nil), &after)
	if after.Revision != own.Revision || after.Draft.Category != "" {
		t.Fatal("batch partially changed own data", after)
	}
}
