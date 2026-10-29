package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLegacyPersonalCollectionHTTP(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := testWorkspaceContext()
	item, err := service.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "资料", Content: "正文"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Publication(ctx, item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	_, key := workspaceLearner(t, h, "legacy-collection")
	_, readKey := managedClient(t, h, "legacy-collection-read", nil)
	_, other := managedLearner(t, h, "legacy-collection-other")
	path := "/api/v1/client/learning/collections/import-legacy"
	date := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	input := map[string]any{"event_id": uuid.NewString(), "collection_id": uuid.NewString(), "title": "旧合集", "items": []string{item.ID}, "created_at": date, "updated_at": date}
	for _, tc := range []struct {
		key  string
		code int
	}{{"", 401}, {adminKey, 401}, {readKey, 401}} {
		if w := request(t, h, "POST", path, tc.key, input); w.Code != tc.code {
			t.Fatal("authority", w.Code, tc.code)
		}
	}
	if w := request(t, h, "POST", path+"?client_id="+uuid.NewString(), key, input); w.Code != 400 {
		t.Fatal("query spoof", w.Code)
	}
	spoof := map[string]any{}
	for k, v := range input {
		spoof[k] = v
	}
	spoof["client_id"] = uuid.NewString()
	if w := request(t, h, "POST", path, key, spoof); w.Code != 400 {
		t.Fatal("body spoof", w.Code)
	}
	var ack study.PersonalCollectionAck
	data(t, request(t, h, "POST", path, key, input), &ack)
	if ack.Duplicate || ack.Collection.Revision != 1 || len(ack.Collection.Items) != 1 {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", path, key, input), &ack)
	if !ack.Duplicate {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", path, other, ownedPayload(t, h, other, item.ID, input)), &ack)
	if ack.Duplicate {
		t.Fatal("client isolation", ack)
	}
	changed := map[string]any{}
	for k, v := range input {
		changed[k] = v
	}
	changed["title"] = "不同标题"
	if w := request(t, h, "POST", path, key, changed); w.Code != 409 {
		t.Fatal("changed event reused", w.Code)
	}
	changed["event_id"] = uuid.NewString()
	if w := request(t, h, "POST", path, key, changed); w.Code != 409 {
		t.Fatal("existing collection overwritten", w.Code)
	}
}
