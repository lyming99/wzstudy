package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLegacyRecordImportHTTP(t *testing.T) {
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
	_, key := workspaceLearner(t, h, "migration-record")
	_, readKey := managedClient(t, h, "migration-record-read", nil)
	_, other := managedLearner(t, h, "migration-record-other")
	path := "/api/v1/client/learning/import-legacy-records"
	created := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	record := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "record_id": uuid.NewString(), "text": "旧记录", "created_at": created, "updated_at": created, "item_revision": item.Published.Revision}
	payload := map[string]any{"records": []any{record}}
	for _, tc := range []struct {
		key  string
		code int
	}{{"", 401}, {adminKey, 401}, {readKey, 401}} {
		if w := request(t, h, "POST", path, tc.key, payload); w.Code != tc.code {
			t.Fatal("authority", w.Code, tc.code)
		}
	}
	if w := request(t, h, "POST", path+"?client_id="+uuid.NewString(), key, payload); w.Code != 400 {
		t.Fatal("query spoof", w.Code)
	}
	if w := request(t, h, "POST", path, key, map[string]any{"records": []any{record}, "client_id": uuid.NewString()}); w.Code != 400 {
		t.Fatal("body spoof", w.Code)
	}
	var ack study.LearningResult
	data(t, request(t, h, "POST", path, key, payload), &ack)
	if ack.Accepted != 1 {
		t.Fatal(ack)
	}
	recordPath := "/api/v1/client/learning/items/" + item.ID + "/records/" + record["record_id"].(string)
	var saved study.RemoteLearningRecord
	data(t, request(t, h, "GET", recordPath, key, nil), &saved)
	if saved.ID != record["record_id"] || saved.Text != "旧记录" {
		t.Fatal(saved)
	}
	if w := request(t, h, "GET", recordPath, other, nil); w.Code != 404 {
		t.Fatal("other learner saw record", w.Code)
	}
	if w := request(t, h, "GET", recordPath, readKey, nil); w.Code != 401 {
		t.Fatal("feedback permission required", w.Code)
	}
	if w := request(t, h, "GET", recordPath+"?client_id="+uuid.NewString(), key, nil); w.Code != 400 {
		t.Fatal("query spoof", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/client/learning/items/"+uuid.NewString()+"/records/"+record["record_id"].(string), key, nil); w.Code != 404 {
		t.Fatal("different item exposed record", w.Code)
	}
	data(t, request(t, h, "POST", path, key, payload), &ack)
	if ack.Duplicates != 1 {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", path, other, ownedPayload(t, h, other, item.ID, payload)), &ack)
	if ack.Accepted != 1 {
		t.Fatal("client isolation", ack)
	}
	changed := map[string]any{}
	for k, v := range record {
		changed[k] = v
	}
	changed["text"] = "不同内容"
	if w := request(t, h, "POST", path, key, map[string]any{"records": []any{changed}}); w.Code != 409 {
		t.Fatal("event conflict", w.Code)
	}
}
