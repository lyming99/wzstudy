package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestClientFeedbackHTTPAuthorizationAndFiltering(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := context.Background()
	item, e := service.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "public feedback candidate", Content: "published body"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	item, e = service.Publication(ctx, item.ID, item.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	client, key := managedClient(t, h, "feedback-learner", []string{"read", "sync", "feedback"})
	_, otherKey := managedClient(t, h, "feedback-other", []string{"read", "sync", "feedback"})
	_, readKey := managedClient(t, h, "feedback-reader", nil)
	_, sourceKey := create(t, h, false)
	event := map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "recommended", "occurred_at": time.Now().UTC().Format(time.RFC3339Nano)}
	body := map[string]any{"events": []any{event}}
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {adminKey, 401}, {sourceKey, 401}, {readKey, 403}} {
		if out := request(t, h, "POST", "/api/v1/client/feedback", tc.key, body); out.Code != tc.status {
			t.Fatal("invalid feedback authority", out.Code, tc.status)
		}
	}
	before, _ := service.Changes(ctx, 0, 100, nil)
	var ack study.FeedbackResult
	data(t, request(t, h, "POST", "/api/v1/client/feedback", key, body), &ack)
	if ack.Accepted != 1 || ack.Duplicates != 0 {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", "/api/v1/client/feedback", key, body), &ack)
	if ack.Accepted != 0 || ack.Duplicates != 1 {
		t.Fatal(ack)
	}
	var candidates study.ClientItemPage
	data(t, request(t, h, "GET", "/api/v1/items?exclude=recommended&include_feedback=true", key, nil), &candidates)
	if len(candidates.Items) != 0 || candidates.ClientID != client.ID {
		t.Fatal(candidates)
	}
	data(t, request(t, h, "GET", "/api/v1/items?unseen=true&include_feedback=true", otherKey, nil), &candidates)
	if len(candidates.Items) != 1 {
		t.Fatal("cross-client feedback leaked", candidates)
	}
	var own study.FeedbackPage
	data(t, request(t, h, "GET", "/api/v1/client/feedback", key, nil), &own)
	if own.Total != 1 || own.Summary["recommended"] != 1 {
		t.Fatal(own)
	}
	var other study.FeedbackPage
	data(t, request(t, h, "GET", "/api/v1/client/feedback", otherKey, nil), &other)
	if other.Total != 0 {
		t.Fatal(other)
	}
	var events study.FeedbackEventPage
	data(t, request(t, h, "GET", "/api/v1/client/feedback/events?after=0&limit=1", key, nil), &events)
	if len(events.Events) != 1 || events.NextCursor != 1 {
		t.Fatal(events)
	}
	data(t, request(t, h, "GET", "/api/v1/admin/clients/"+client.ID+"/items?exclude=recommended", adminKey, nil), &candidates)
	if len(candidates.Items) != 0 {
		t.Fatal(candidates)
	}
	data(t, request(t, h, "GET", "/api/v1/admin/clients/"+client.ID+"/feedback", adminKey, nil), &own)
	if own.Total != 1 {
		t.Fatal(own)
	}
	for _, path := range []string{"/api/v1/admin/clients/" + client.ID + "/feedback", "/api/v1/admin/clients/" + client.ID + "/items"} {
		if out := request(t, h, "GET", path, key, nil); out.Code != 401 {
			t.Fatal("client accessed admin preview", path, out.Code)
		}
	}
	for _, path := range []string{"/api/v1/items?exclude=unknown", "/api/v1/items?unseen=perhaps", "/api/v1/items?exclude=recommended,recommended", "/api/v1/items?client_id=" + client.ID, "/api/v1/client/feedback?client_id=" + client.ID, "/api/v1/client/feedback/events?after=-1"} {
		if out := request(t, h, "GET", path, key, nil); out.Code != 400 {
			t.Fatal(path, out.Code)
		}
	}
	if out := request(t, h, "POST", "/api/v1/client/feedback", key, map[string]any{"client_id": client.ID, "events": []any{event}}); out.Code != 400 {
		t.Fatal("spoofed client id accepted", out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/client/feedback", adminKey, nil); out.Code != 401 {
		t.Fatal("admin key used as client identity", out.Code)
	}
	after, _ := service.Changes(ctx, 0, 100, nil)
	if before.HighWatermark != after.HighWatermark {
		t.Fatal("feedback changes entered public feed")
	}
}

func TestFeedbackQRPermissionsAndPrivateDraft(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	ctx := context.Background()
	client, key := managedClient(t, h, "learner-qr", []string{"read", "feedback"})
	out := request(t, h, "GET", "/api/v1/client/connection", key, nil)
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"feedback"`) {
		t.Fatal(out.Code)
	}
	_, feedbackOnly := managedClient(t, h, "feedback-only", []string{"feedback"})
	if out := request(t, h, "GET", "/api/v1/client/connection", feedbackOnly, nil); out.Code != 403 {
		t.Fatal("report-only key paired as query reader", out.Code)
	}
	draft, e := service.SaveManaged(ctx, "", study.ManagedInput{Status: "draft", Content: study.Content{Title: "do not expose draft", Content: "private"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	event := map[string]any{"event_id": uuid.NewString(), "item_id": draft.ID, "action": "record", "note": "learning note"}
	if out := request(t, h, "POST", "/api/v1/client/feedback", key, map[string]any{"events": []any{event}}); out.Code != 404 {
		t.Fatal(out.Code)
	}
	var state study.FeedbackPage
	data(t, request(t, h, "GET", "/api/v1/admin/clients/"+client.ID+"/feedback", adminKey, nil), &state)
	if state.Total != 0 || state.HighWatermark != 0 {
		t.Fatal(state)
	}
	var updated study.Client
	data(t, request(t, h, "PUT", "/api/v1/admin/clients/"+client.ID, adminKey, policyBody(client, []string{"read"})), &updated)
	if out := request(t, h, "POST", "/api/v1/client/feedback", key, map[string]any{"events": []any{event}}); out.Code != 403 {
		t.Fatal("revocation did not apply", out.Code)
	}
}
