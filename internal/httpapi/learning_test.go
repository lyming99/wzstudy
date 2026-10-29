package httpapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLearningHTTPAuthorizationAndIsolation(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	item, err := service.SaveManaged(testWorkspaceContext(), "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "公开资料", Kind: "article", Content: "学习正文"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Publication(testWorkspaceContext(), item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	client, key := workspaceLearner(t, h, "learning-own")
	_, other := managedLearner(t, h, "learning-other")
	_, reader := managedClient(t, h, "learning-read", []string{"read"})
	_, sourceKey := create(t, h, false)
	var capabilities struct {
		ClientID string `json:"client_id"`
		Read     bool   `json:"read"`
		Write    bool   `json:"write"`
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning/capabilities", key, nil), &capabilities)
	if capabilities.ClientID != client.ID || !capabilities.Read || !capabilities.Write {
		t.Fatalf("capabilities: %+v", capabilities)
	}
	if w := request(t, h, "GET", "/api/v1/client/learning/capabilities", reader, nil); w.Code != 401 {
		t.Fatalf("query key became learner: %d", w.Code)
	}
	for _, invalid := range []string{"", adminKey, sourceKey} {
		if w := request(t, h, "GET", "/api/v1/client/learning/capabilities", invalid, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("invalid learner identity: %d %s", w.Code, w.Body.String())
		}
	}
	recordID := uuid.NewString()
	viewID := uuid.NewString()
	payload := map[string]any{"events": []any{
		map[string]any{"event_id": viewID, "item_id": item.ID, "action": "view"},
		map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "record_create", "record_id": recordID, "text": "私人的记录"},
		map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "progress", "mode": "preview", "expected_revision": 0, "position": map[string]any{"kind": "article", "version": item.Published.Revision, "block_id": "body", "offset": 1}},
	}}
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {adminKey, 401}, {sourceKey, 401}, {reader, 401}} {
		if w := request(t, h, "POST", "/api/v1/client/learning/events", tc.key, payload); w.Code != tc.status {
			t.Fatalf("write authority: %d %s", w.Code, w.Body.String())
		}
	}
	var ack study.LearningResult
	data(t, request(t, h, "POST", "/api/v1/client/learning/events", key, payload), &ack)
	if ack.Accepted != 3 || len(ack.States) != 1 || ack.States[0].ViewCount != 1 {
		t.Fatalf("ack: %+v", ack)
	}
	data(t, request(t, h, "POST", "/api/v1/client/learning/events", key, payload), &ack)
	if ack.Duplicates != 3 {
		t.Fatalf("retry: %+v", ack)
	}
	var own, otherState study.LearningPage
	data(t, request(t, h, "GET", "/api/v1/client/learning?item_id="+item.ID, key, nil), &own)
	data(t, request(t, h, "GET", "/api/v1/client/learning?item_id="+item.ID, other, nil), &otherState)
	if own.Total != 1 || otherState.Total != 0 {
		t.Fatalf("identity leaked: %+v %+v", own, otherState)
	}
	var records study.LearningRecordPage
	data(t, request(t, h, "GET", "/api/v1/client/learning/items/"+item.ID+"/records", key, nil), &records)
	if records.Total != 1 || records.Items[0].ID != recordID {
		t.Fatalf("records: %+v", records)
	}
	if w := request(t, h, "GET", "/api/v1/admin/users/"+client.ID+"/learning", adminKey, nil); w.Code != 404 {
		t.Fatal("administrator can browse private learning", w.Code)
	}
	for _, path := range []string{"/api/v1/client/learning?client_id=" + client.ID, "/api/v1/client/learning/events?client_id=" + client.ID, "/api/v1/client/learning/items/" + item.ID + "/records?client_id=" + client.ID} {
		if w := request(t, h, "GET", path, key, nil); w.Code != 400 {
			t.Fatalf("spoofed identity: %s %d", path, w.Code)
		}
	}
	spoof := map[string]any{"client_id": client.ID, "events": payload["events"]}
	if w := request(t, h, "POST", "/api/v1/client/learning/events", key, spoof); w.Code != 400 {
		t.Fatalf("spoofed body: %d", w.Code)
	}
	var legacy study.FeedbackPage
	data(t, request(t, h, "GET", "/api/v1/client/feedback", key, nil), &legacy)
	if legacy.Total != 0 || legacy.HighWatermark != 0 {
		t.Fatalf("v1 feedback changed: %+v", legacy)
	}
	for _, likeKey := range []string{key} {
		like := map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "like"}}}
		data(t, request(t, h, "POST", "/api/v1/client/feedback", likeKey, like), new(study.FeedbackResult))
	}
	var stats study.LearningItemStats
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID+"/learning-stats", reader, nil), &stats)
	if stats.LikeCount != 1 || stats.ViewCount != 1 || stats.RecordCount != 1 {
		t.Fatalf("aggregate counts: %+v", stats)
	}
	_, syncOnly := managedClient(t, h, "learning-sync-stats", []string{"sync"})
	_, feedbackOnly := managedClient(t, h, "learning-feedback-stats", []string{"feedback"})
	statsPath := "/api/v1/client/learning/items/" + item.ID + "/stats"
	for _, allowed := range []string{key, reader, syncOnly} {
		var isolated study.LearningItemStats
		data(t, request(t, h, "GET", statsPath, allowed, nil), &isolated)
		if isolated.ItemID != item.ID || isolated.ViewCount != 1 || isolated.RecordCount != 1 {
			t.Fatalf("client aggregate stats: %+v", isolated)
		}
	}
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {adminKey, 401}, {sourceKey, 401}, {feedbackOnly, 403}} {
		if w := request(t, h, "GET", statsPath, tc.key, nil); w.Code != tc.status {
			t.Fatalf("client stats authorization: %d %s", w.Code, w.Body.String())
		}
	}
	unlike := map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "like", "value": false}}}
	data(t, request(t, h, "POST", "/api/v1/client/feedback", key, unlike), new(study.FeedbackResult))
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID+"/learning-stats", reader, nil), &stats)
	if stats.LikeCount != 0 {
		t.Fatalf("cancelled like counted: %+v", stats)
	}
	var updated study.User
	data(t, request(t, h, "PUT", "/api/v1/admin/users/"+client.ID, adminKey, map[string]any{"revision": client.Revision, "email": client.Email, "display_name": client.DisplayName, "enabled": false}), &updated)
	if w := request(t, h, "POST", "/api/v1/client/learning/events", key, payload); w.Code != 401 {
		t.Fatalf("revocation did not apply: %d", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/client/learning", key, nil); w.Code != 401 {
		t.Fatalf("revoked identity read v2 data: %d", w.Code)
	}
}
