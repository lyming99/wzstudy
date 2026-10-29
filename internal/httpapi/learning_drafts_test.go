package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLearningDraftHTTP(t *testing.T) {
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
	_, key := workspaceLearner(t, h, "draft-a")
	_, reader := managedClient(t, h, "draft-reader", nil)
	_, other := managedLearner(t, h, "draft-b")
	readPath := "/api/v1/client/learning/items/" + item.ID + "/draft"
	commandPath := "/api/v1/client/learning/drafts/commands"
	importPath := "/api/v1/client/learning/drafts/import-legacy"
	command := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "text": "私人草稿", "expected_revision": 0}
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {adminKey, 401}, {reader, 401}} {
		if w := request(t, h, "GET", readPath, tc.key, nil); w.Code != tc.status {
			t.Fatal("read authority", w.Code, tc.status)
		}
		if w := request(t, h, "POST", commandPath, tc.key, command); w.Code != tc.status {
			t.Fatal("write authority", w.Code, tc.status)
		}
	}
	if w := request(t, h, "GET", readPath+"?client_id="+uuid.NewString(), key, nil); w.Code != 400 {
		t.Fatal("query spoof", w.Code)
	}
	spoof := map[string]any{}
	for k, v := range command {
		spoof[k] = v
	}
	spoof["client_id"] = uuid.NewString()
	if w := request(t, h, "POST", commandPath, key, spoof); w.Code != 400 {
		t.Fatal("body spoof", w.Code)
	}
	var ack study.LearningDraftAck
	data(t, request(t, h, "POST", commandPath, key, command), &ack)
	if ack.Draft.Text != "私人草稿" || ack.Draft.Revision != 1 || ack.Duplicate {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", commandPath, key, command), &ack)
	if !ack.Duplicate {
		t.Fatal(ack)
	}
	var page study.LearningDraftRead
	data(t, request(t, h, "GET", readPath, key, nil), &page)
	if page.Draft == nil || page.Draft.Text != "私人草稿" {
		t.Fatal(page)
	}
	data(t, request(t, h, "GET", readPath, other, nil), &page)
	if page.Draft != nil {
		t.Fatal("cross client leak", page)
	}
	legacy := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "text": "旧私人草稿", "item_revision": item.Published.Revision, "updated_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)}
	if w := request(t, h, "POST", importPath, key, legacy); w.Code != 409 {
		t.Fatal("overwrote draft", w.Code)
	}
	if w := request(t, h, "POST", importPath, other, legacy); w.Code != 404 {
		t.Fatal("foreign draft import accepted", w.Code)
	}
	otherLegacy := ownedPayload(t, h, other, item.ID, legacy)
	data(t, request(t, h, "POST", importPath, other, otherLegacy), &ack)
	if ack.Duplicate || ack.Draft.Text != "旧私人草稿" {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", importPath, other, otherLegacy), &ack)
	if !ack.Duplicate {
		t.Fatal(ack)
	}
	eventPath := "/api/v1/client/learning/drafts/events/" + command["event_id"].(string)
	var retained study.LearningDraft
	data(t, request(t, h, "GET", eventPath, key, nil), &retained)
	if retained.Text != "私人草稿" {
		t.Fatal(retained)
	}
	if w := request(t, h, "GET", eventPath, other, nil); w.Code != 404 {
		t.Fatal("history leaked", w.Code)
	}
	if w := request(t, h, "GET", eventPath+"?client_id="+uuid.NewString(), key, nil); w.Code != 400 {
		t.Fatal("history query spoof", w.Code)
	}
	historyPath := readPath + "/history?limit=1&offset=0"
	var history study.LearningDraftHistory
	data(t, request(t, h, "GET", historyPath, key, nil), &history)
	if history.Total != 1 || len(history.Items) != 1 || history.Items[0].Text != "私人草稿" {
		t.Fatal(history)
	}
	if w := request(t, h, "GET", readPath+"/history?limit=101", key, nil); w.Code != 400 {
		t.Fatal("history page limit", w.Code)
	}
}
