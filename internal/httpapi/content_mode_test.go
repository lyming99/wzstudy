package httpapi

import (
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestContentModeHTTPRoundTripFilteringAndMetadataIsolation(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := testWorkspaceContext()
	_, reader := managedClient(t, h, "mode-reader", []string{"read"})
	var item study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items", workspaceKey, map[string]any{
		"status": "pending", "content": map[string]any{"title": "模式字段", "content": "已公开正文", "category": "人工智能", "content_mode": "learning"},
	}), &item)
	var e error
	item, e = service.Publication(ctx, item.ID, item.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	var public study.PublicItem
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID, reader, nil), &public)
	if public.ContentMode != "learning" || public.Category != "人工智能" {
		t.Fatal(public)
	}
	var news study.LearningModePage
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news", reader, nil), &news)
	if news.Total != 0 {
		t.Fatal("learning material returned in news", news)
	}
	var query struct {
		Items []study.PublicItem `json:"items"`
	}
	data(t, request(t, h, "GET", "/api/v1/items?content_mode=learning&category=人工智能", reader, nil), &query)
	if len(query.Items) != 1 {
		t.Fatal(query)
	}
	data(t, request(t, h, "GET", "/api/v1/items?content_mode=default", reader, nil), &query)
	if len(query.Items) != 0 {
		t.Fatal("default filter included learning", query)
	}
	if w := request(t, h, "GET", "/api/v1/items?content_mode=invalid", reader, nil); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Pending body/title changes remain private when only the mode is changed.
	c := item.Draft
	c.Title = "私密标题"
	c.Content = "私密待审正文"
	item, e = service.SaveManaged(ctx, item.ID, study.ManagedInput{Revision: item.Revision, SourceID: item.SourceID, Status: "pending", Content: c}, nil)
	if e != nil {
		t.Fatal(e)
	}
	body := map[string]any{"items": []any{map[string]any{"id": item.ID, "revision": item.Revision}}, "content_mode": "news"}
	if w := request(t, h, "POST", "/api/v1/workspace/items/organize", reader, body); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	var receipt study.ItemOrganizationResult
	data(t, request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, body), &receipt)
	if receipt.ModeUpdated != 1 || receipt.CategoryUpdated != 0 {
		t.Fatal(receipt)
	}
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID, reader, nil), &public)
	if public.ContentMode != "news" || public.Category != "人工智能" || public.Title != "模式字段" || public.Content.Content != "已公开正文" {
		t.Fatal("mode setter published private edits", public)
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news", reader, nil), &news)
	if news.Total != 1 {
		t.Fatal(news)
	}
	body["items"] = []any{map[string]any{"id": item.ID, "revision": receipt.Items[0].Revision}}
	body["content_mode"] = "invalid"
	if w := request(t, h, "POST", "/api/v1/workspace/items/organize", workspaceKey, body); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	var legacy study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items", workspaceKey, map[string]any{"status": "pending", "content": map[string]any{"title": "旧版载荷", "content": "正文", "category": "设计灵感"}}), &legacy)
	legacy, e = service.Publication(ctx, legacy.ID, legacy.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	data(t, request(t, h, "GET", "/api/v1/items?content_mode=default", reader, nil), &query)
	if len(query.Items) != 1 || query.Items[0].ID != legacy.ID {
		t.Fatal("legacy record not treated as default", query)
	}
}
