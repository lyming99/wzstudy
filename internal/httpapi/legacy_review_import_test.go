package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLegacyReviewImportHTTP(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := context.Background()
	item, err := service.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "资料", Content: "正文"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Publication(ctx, item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	_, key := managedClient(t, h, "migration-review", []string{"read", "feedback"})
	_, readKey := managedClient(t, h, "migration-review-read", nil)
	_, other := managedClient(t, h, "migration-review-other", []string{"read", "feedback"})
	path := "/api/v1/client/learning/import-legacy-reviews"
	entry := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "item_revision": item.Published.Revision, "occurred_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)}
	body := map[string]any{"reviews": []any{entry}}
	for _, tc := range []struct {
		key  string
		code int
	}{{"", 401}, {adminKey, 401}, {readKey, 403}} {
		if w := request(t, h, "POST", path, tc.key, body); w.Code != tc.code {
			t.Fatal("authority", w.Code, tc.code)
		}
	}
	if w := request(t, h, "POST", path+"?client_id="+uuid.NewString(), key, body); w.Code != 400 {
		t.Fatal("query spoof", w.Code)
	}
	if w := request(t, h, "POST", path, key, map[string]any{"reviews": []any{entry}, "client_id": uuid.NewString()}); w.Code != 400 {
		t.Fatal("body spoof", w.Code)
	}
	var ack study.LearningResult
	data(t, request(t, h, "POST", path, key, body), &ack)
	if ack.Accepted != 1 || !ack.States[0].ReviewPending || ack.States[0].ReviewCount != 0 {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", path, key, body), &ack)
	if ack.Duplicates != 1 {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", path, other, body), &ack)
	if ack.Accepted != 1 {
		t.Fatal("client isolation", ack)
	}
	changed := map[string]any{}
	for k, v := range entry {
		changed[k] = v
	}
	changed["event_id"] = uuid.NewString()
	if w := request(t, h, "POST", path, key, map[string]any{"reviews": []any{changed}}); w.Code != 409 {
		t.Fatal("existing state overwritten", w.Code)
	}
}
