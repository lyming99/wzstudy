package httpapi

import (
	"context"
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestRecommendationHTTPReadOnlyAndIdentity(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	item, err := service.SaveManaged(context.Background(), "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "学习内容"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publication(context.Background(), item.ID, item.Revision, true); err != nil {
		t.Fatal(err)
	}
	_, reader := managedClient(t, h, "recommend-reader", []string{"read"})
	_, other := managedClient(t, h, "recommend-other", []string{"read"})
	_, source := create(t, h, false)
	for _, invalid := range []string{"", adminKey, readKey, source} {
		if w := request(t, h, "GET", "/api/v1/client/recommendations", invalid, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("invalid identity: %d %s", w.Code, w.Body.String())
		}
	}
	var page study.RecommendationPage
	data(t, request(t, h, "GET", "/api/v1/client/recommendations?limit=1", reader, nil), &page)
	if len(page.Items) != 1 || page.Items[0].ID != item.ID || page.HasMore {
		t.Fatalf("page: %+v", page)
	}
	if w := request(t, h, "GET", "/api/v1/client/recommendations?client_id=other", reader, nil); w.Code != 400 {
		t.Fatalf("spoofed identity: %d", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/client/recommendations?limit=0", reader, nil); w.Code != 400 {
		t.Fatalf("invalid limit: %d", w.Code)
	}
	var feedback study.FeedbackEventPage
	data(t, request(t, h, "GET", "/api/v1/client/feedback/events", other, nil), &feedback)
	if feedback.HighWatermark != 0 {
		t.Fatalf("query wrote feedback: %+v", feedback)
	}
}
