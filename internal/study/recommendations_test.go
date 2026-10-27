package study

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecommendationsReadOnlyScopedStableAndRevocable(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	now := time.Now().UTC()
	s.Now = func() time.Time { return now }
	client, key := feedbackClient(t, s, []string{"read", "feedback"})
	_, other := feedbackClient(t, s, []string{"read", "feedback"})
	first := approveManaged(t, s, managedArticle(t, s, "pending", "first"))
	second := approveManaged(t, s, managedArticle(t, s, "pending", "second"))
	third := approveManaged(t, s, managedArticle(t, s, "pending", "third"))
	_ = first
	_ = third
	private := managedArticle(t, s, "draft", "private")
	pending := true
	if _, err := s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: second.ID, Action: "review_plan", Value: &pending}}); err != nil {
		t.Fatal(err)
	}
	beforeFeed, _ := s.Changes(ctx, 0, 100, nil)
	beforeFeedback, _ := s.FeedbackEvents(ctx, key, "", 0, 100)
	beforeLearning, _ := s.ClientLearningEvents(ctx, key, "", 0, 100)
	page, err := s.Recommendations(ctx, key, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if _, err = s.Recommendations(ctx, other, page.NextCursor, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-client cursor: %v", err)
	}
	if _, err = s.Recommendations(ctx, "", "", 1); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("anonymous query: %v", err)
	}
	afterFeed, _ := s.Changes(ctx, 0, 100, nil)
	afterFeedback, _ := s.FeedbackEvents(ctx, key, "", 0, 100)
	afterLearning, _ := s.ClientLearningEvents(ctx, key, "", 0, 100)
	if beforeFeed.HighWatermark != afterFeed.HighWatermark || beforeFeedback.HighWatermark != afterFeedback.HighWatermark || beforeLearning.HighWatermark != afterLearning.HighWatermark {
		t.Fatal("recommendation query created a persistent event")
	}
	id, offset, err := parseRecommendationCursor(page.NextCursor)
	if err != nil || offset != 1 {
		t.Fatal(id, offset, err)
	}
	s.recommendMu.Lock()
	session := s.recommendations[id]
	s.recommendMu.Unlock()
	if len(session.ItemIDs) != 3 {
		t.Fatalf("private or missing candidates: %+v", session.ItemIDs)
	}
	for _, candidate := range session.ItemIDs {
		if candidate == private.ID {
			t.Fatal("private item recommended")
		}
	}
	withdrawnID := session.ItemIDs[1]
	withdrawn, err := s.Item(ctx, withdrawnID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publication(ctx, withdrawnID, withdrawn.Revision, false); err != nil {
		t.Fatal(err)
	}
	page2, err := s.Recommendations(ctx, key, page.NextCursor, 1)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].ID == withdrawnID || page2.Items[0].ID == page.Items[0].ID || page2.HasMore {
		t.Fatalf("withdrawn candidate page: %+v %v", page2, err)
	}

	fresh, err := s.Recommendations(ctx, key, "", 1)
	if err != nil || !fresh.HasMore {
		t.Fatalf("fresh: %+v %v", fresh, err)
	}
	_, err = s.UpdateClient(ctx, client.ID, client.Revision, ClientInput{Slug: client.Slug, Name: client.Name, Permissions: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Recommendations(ctx, key, fresh.NextCursor, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("permission change not detected: %v", err)
	}
	fresh, err = s.Recommendations(ctx, key, "", 1)
	if err != nil || !fresh.HasMore {
		t.Fatalf("read-only recommendation: %+v %v", fresh, err)
	}
	now = now.Add(recommendationTTL + time.Second)
	if _, err = s.Recommendations(ctx, key, fresh.NextCursor, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired cursor: %v", err)
	}
}
