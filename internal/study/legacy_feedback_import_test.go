package study

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLegacyFeedbackImportIsConditionalAtomicAndIdempotent(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			var store Store = NewMemoryStore()
			if kind == "file" {
				var err error
				store, err = OpenFileStore(filepath.Join(t.TempDir(), "workspace.json"))
				if err != nil {
					t.Fatal(err)
				}
			}
			s := NewService(store)
			ctx := context.Background()
			now := time.Now().UTC().Round(0)
			s.Now = func() time.Time { return now }
			_, key := feedbackClient(t, s, []string{"read", "feedback"})
			_, otherKey := feedbackClient(t, s, []string{"read", "feedback"})
			item := approveManaged(t, s, managedArticle(t, s, "pending", "迁移资料"))
			at := now.Add(-time.Hour)
			makeInput := func(action string, value bool) FeedbackInput {
				return FeedbackInput{EventID: uuid.NewString(), ItemID: item.ID, Action: action,
					Value: feedbackBool(value), ItemRevision: item.Published.Revision, OccurredAt: &at}
			}
			// Inactive is still a deliberate server choice; it must not be overwritten.
			existing := feedbackInput(item, "favorite", now)
			existing.Value = feedbackBool(false)
			if _, err := s.ReportFeedback(ctx, key, []FeedbackInput{existing}); err != nil {
				t.Fatal(err)
			}
			like, favorite := makeInput("like", true), makeInput("favorite", true)
			if _, err := s.ImportLegacyFeedback(ctx, key, []FeedbackInput{like, favorite}); !errors.Is(err, ErrConflict) {
				t.Fatal("expected existing-action conflict", err)
			}
			state, err := s.ClientFeedback(ctx, key, "", []string{item.ID}, 20, 0)
			if err != nil || len(state.Items) != 1 || state.Items[0].Actions["favorite"].Active {
				t.Fatal(state, err)
			}
			if _, ok := state.Items[0].Actions["like"]; ok {
				t.Fatal("failed batch partly committed")
			}
			events, err := s.FeedbackEvents(ctx, key, "", 0, 100)
			if err != nil || events.HighWatermark != 1 {
				t.Fatal("failed batch changed clock", events, err)
			}
			result, err := s.ImportLegacyFeedback(ctx, key, []FeedbackInput{like})
			if err != nil || result.Accepted != 1 {
				t.Fatal(result, err)
			}
			retry, err := s.ImportLegacyFeedback(ctx, key, []FeedbackInput{like})
			if err != nil || retry.Duplicates != 1 || retry.Accepted != 0 {
				t.Fatal(retry, err)
			}
			changed := like
			changed.Value = feedbackBool(false)
			if _, err = s.ImportLegacyFeedback(ctx, key, []FeedbackInput{changed}); !errors.Is(err, ErrConflict) {
				t.Fatal("same event id with changed input", err)
			}
			if result, err = s.ImportLegacyFeedback(ctx, otherKey, []FeedbackInput{like}); err != nil || result.Accepted != 1 {
				t.Fatal("other client isolation", result, err)
			}
			invalid := makeInput("read", true)
			if _, err = s.ImportLegacyFeedback(ctx, key, []FeedbackInput{invalid}); !errors.Is(err, ErrInvalid) {
				t.Fatal("unsupported historical event", err)
			}
			missing := makeInput("favorite", true)
			missing.OccurredAt = nil
			if _, err = s.ImportLegacyFeedback(ctx, key, []FeedbackInput{missing}); !errors.Is(err, ErrInvalid) {
				t.Fatal("missing historical timestamp", err)
			}
			versioned := approveManaged(t, s, managedArticle(t, s, "pending", "版本变更"))
			oldRevision := versioned.Published.Revision
			versioned, err = s.SaveManaged(ctx, versioned.ID, ManagedInput{Revision: versioned.Revision, Status: "pending", Content: Content{Title: "新版本", Kind: "article", Content: "新版正文", Category: "学习方法", Author: "管理员"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			versioned = approveManaged(t, s, versioned)
			stale := FeedbackInput{EventID: uuid.NewString(), ItemID: versioned.ID, Action: "like", Value: feedbackBool(true), ItemRevision: oldRevision, OccurredAt: &at}
			if _, err = s.ImportLegacyFeedback(ctx, key, []FeedbackInput{stale}); !errors.Is(err, ErrConflict) {
				t.Fatal("changed content revision must conflict", err)
			}
			concurrent := approveManaged(t, s, managedArticle(t, s, "pending", "并发迁移"))
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					input := FeedbackInput{EventID: uuid.NewString(), ItemID: concurrent.ID, Action: "like", Value: feedbackBool(true), ItemRevision: concurrent.Published.Revision, OccurredAt: &at}
					_, err := s.ImportLegacyFeedback(ctx, key, []FeedbackInput{input})
					results <- err
				}()
			}
			wg.Wait()
			close(results)
			succeeded, conflicted := 0, 0
			for err := range results {
				if err == nil {
					succeeded++
				} else if errors.Is(err, ErrConflict) {
					conflicted++
				} else {
					t.Fatal(err)
				}
			}
			if succeeded != 1 || conflicted != 1 {
				t.Fatal("concurrent import was not conditional", succeeded, conflicted)
			}
		})
	}
}
