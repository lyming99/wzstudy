package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestImportLegacyFeedbackHTTP(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := testWorkspaceContext()
	item, err := service.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "可迁移资料", Content: "公开正文"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Publication(ctx, item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	_, key := workspaceLearner(t, h, "migration-client")
	_, other := managedLearner(t, h, "migration-other")
	_, readOnly := managedClient(t, h, "migration-reader", nil)
	path := "/api/v1/client/feedback/import-legacy"
	event := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "like", "value": true,
		"item_revision": item.Published.Revision, "occurred_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)}
	body := map[string]any{"events": []any{event}}
	for _, tc := range []struct {
		key  string
		code int
	}{{"", 401}, {adminKey, 401}, {readOnly, 401}} {
		if out := request(t, h, "POST", path, tc.key, body); out.Code != tc.code {
			t.Fatal("authority", out.Code, tc.code)
		}
	}
	if out := request(t, h, "POST", path+"?client_id="+uuid.NewString(), key, body); out.Code != 400 {
		t.Fatal("query identity spoof", out.Code)
	}
	if out := request(t, h, "POST", path, key, map[string]any{"events": []any{event}, "client_id": uuid.NewString()}); out.Code != 400 {
		t.Fatal("body identity spoof", out.Code)
	}
	var result study.FeedbackResult
	data(t, request(t, h, "POST", path, key, body), &result)
	if result.Accepted != 1 {
		t.Fatal(result)
	}
	data(t, request(t, h, "POST", path, key, body), &result)
	if result.Duplicates != 1 || result.Accepted != 0 {
		t.Fatal(result)
	}
	conflicting := map[string]any{}
	for k, v := range event {
		conflicting[k] = v
	}
	conflicting["event_id"] = uuid.NewString()
	conflicting["value"] = false
	if out := request(t, h, "POST", path, key, map[string]any{"events": []any{conflicting}}); out.Code != 409 {
		t.Fatal("existing server choice overwritten", out.Code)
	}
	data(t, request(t, h, "POST", path, other, ownedPayload(t, h, other, item.ID, body)), &result)
	if result.Accepted != 1 {
		t.Fatal("client isolation", result)
	}
	bad := map[string]any{}
	for k, v := range event {
		bad[k] = v
	}
	bad["event_id"] = uuid.NewString()
	bad["action"] = "read"
	if out := request(t, h, "POST", path, other, map[string]any{"events": []any{bad}}); out.Code != 400 {
		t.Fatal("unsupported history", out.Code)
	}
}
