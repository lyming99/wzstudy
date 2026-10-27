package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLearningDraftHTTP(t *testing.T) {
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
	_, key := managedClient(t, h, "draft-a", []string{"read", "feedback"})
	_, reader := managedClient(t, h, "draft-reader", nil)
	_, other := managedClient(t, h, "draft-b", []string{"read", "feedback"})
	readPath := "/api/v1/client/learning/items/" + item.ID + "/draft"
	commandPath := "/api/v1/client/learning/drafts/commands"
	importPath := "/api/v1/client/learning/drafts/import-legacy"
	command := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "text": "私人草稿", "expected_revision": 0}
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {adminKey, 401}, {reader, 403}} {
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
	data(t, request(t, h, "POST", importPath, other, legacy), &ack)
	if ack.Duplicate || ack.Draft.Text != "旧私人草稿" {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", importPath, other, legacy), &ack)
	if !ack.Duplicate {
		t.Fatal(ack)
	}
}
