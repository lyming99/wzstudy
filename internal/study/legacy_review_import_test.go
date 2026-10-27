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

func TestLegacyReviewImportProtectsCurrentStateAndHistory(t *testing.T) {
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
			_, other := feedbackClient(t, s, []string{"read", "feedback"})
			_, reader := feedbackClient(t, s, []string{"read"})
			item := approveManaged(t, s, managedArticle(t, s, "pending", "待复习"))
			at := now.Add(-time.Hour)
			input := LegacyReviewInput{EventID: uuid.NewString(), ItemID: item.ID, ItemRevision: item.Published.Revision, OccurredAt: &at}
			if _, err := s.ImportLegacyReviews(ctx, reader, []LegacyReviewInput{input}); !errors.Is(err, ErrForbidden) {
				t.Fatal("reader granted write", err)
			}
			out, err := s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{input})
			if err != nil || out.Accepted != 1 || len(out.States) != 1 {
				t.Fatal(out, err)
			}
			state := out.States[0]
			if !state.ReviewPending || state.ReviewCount != 0 || state.ReviewRevision != 1 || state.ReviewLastEventID != input.EventID {
				t.Fatal("plan fabricated or wrong revision", state)
			}
			retry, err := s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{input})
			if err != nil || retry.Duplicates != 1 || retry.Accepted != 0 {
				t.Fatal(retry, err)
			}
			changed := input
			changed.ItemRevision++
			if _, err = s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{changed}); !errors.Is(err, ErrConflict) {
				t.Fatal("event id reused with different version", err)
			}
			if out, err = s.ImportLegacyReviews(ctx, other, []LegacyReviewInput{input}); err != nil || out.Accepted != 1 {
				t.Fatal("client isolation", out, err)
			}
			second := approveManaged(t, s, managedArticle(t, s, "pending", "已有撤销"))
			off := false
			prior := LearningEventInput{EventID: uuid.NewString(), ItemID: second.ID, Action: "review_plan", Value: &off}
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{prior}); err != nil {
				t.Fatal(err)
			}
			conflicting := LegacyReviewInput{EventID: uuid.NewString(), ItemID: second.ID, ItemRevision: second.Published.Revision, OccurredAt: &at}
			clean := approveManaged(t, s, managedArticle(t, s, "pending", "原子回滚"))
			first := LegacyReviewInput{EventID: uuid.NewString(), ItemID: clean.ID, ItemRevision: clean.Published.Revision, OccurredAt: &at}
			if _, err = s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{first, conflicting}); !errors.Is(err, ErrConflict) {
				t.Fatal("existing disabled plan overwritten", err)
			}
			page, err := s.ClientLearning(ctx, key, "", []string{clean.ID}, 20, 0)
			if err != nil || len(page.Items) != 0 {
				t.Fatal("batch partially committed", page, err)
			}
			badVersion := first
			badVersion.ItemRevision++
			if _, err = s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{badVersion}); !errors.Is(err, ErrConflict) {
				t.Fatal("version changed", err)
			}
			missing := first
			missing.OccurredAt = nil
			if _, err = s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{missing}); !errors.Is(err, ErrInvalid) {
				t.Fatal("missing time", err)
			}
			racing := approveManaged(t, s, managedArticle(t, s, "pending", "并发计划"))
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					x := LegacyReviewInput{EventID: uuid.NewString(), ItemID: racing.ID, ItemRevision: racing.Published.Revision, OccurredAt: &at}
					_, err := s.ImportLegacyReviews(ctx, key, []LegacyReviewInput{x})
					results <- err
				}()
			}
			wg.Wait()
			close(results)
			success, conflict := 0, 0
			for err := range results {
				if err == nil {
					success++
				} else if errors.Is(err, ErrConflict) {
					conflict++
				} else {
					t.Fatal(err)
				}
			}
			if success != 1 || conflict != 1 {
				t.Fatal("conditional concurrency", success, conflict)
			}
		})
	}
}
