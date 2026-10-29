package feed

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"wzstudy/internal/study"
)

type workspaceFetcher struct{ calls int }

func (f *workspaceFetcher) Fetch(ctx context.Context, source study.Source) (Result, error) {
	f.calls++
	if study.WorkspaceUserID(ctx) != source.OwnerID {
		return Result{}, study.ErrForbidden
	}
	return Result{Items: []study.Content{{ExternalID: "same-feed-entry", Title: source.Name, Content: "owned RSS body"}}}, nil
}
func TestBackgroundRSSUsesSourceOwnerWorkspace(t *testing.T) {
	ctx := context.Background()
	service := study.NewService(study.NewMemoryStore())
	a, err := service.CreateUser(ctx, study.UserInput{Email: "rss-a@example.test", DisplayName: "A", Password: "rss-user-password-a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.CreateUser(ctx, study.UserInput{Email: "rss-b@example.test", DisplayName: "B", Password: "rss-user-password-b"})
	if err != nil {
		t.Fatal(err)
	}
	ac, bc := study.WithWorkspace(ctx, a.ID), study.WithWorkspace(ctx, b.ID)
	as, _, err := service.CreateSource(ac, study.SourceInput{Slug: "rss", Name: "A RSS", Kind: "rss", FeedURL: "https://a.example.test/feed"})
	if err != nil {
		t.Fatal(err)
	}
	bs, _, err := service.CreateSource(bc, study.SourceInput{Slug: "rss", Name: "B RSS", Kind: "rss", FeedURL: "https://b.example.test/feed"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.CreateSource(ctx, study.SourceInput{Slug: "legacy-rss", Name: "unowned archive", Kind: "rss", FeedURL: "https://legacy.example.test/feed"})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &workspaceFetcher{}
	worker := &Worker{Service: service, Fetcher: fetcher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	worker.poll(ctx)
	if fetcher.calls != 2 {
		t.Fatal("unowned source was polled or user source skipped", fetcher.calls)
	}
	for _, tc := range []struct {
		ctx   context.Context
		title string
	}{{ac, "A RSS"}, {bc, "B RSS"}} {
		items, err := service.Items(tc.ctx, study.Filter{Limit: 20})
		if err != nil || len(items) != 1 || items[0].Published.Title != tc.title {
			t.Fatal("RSS owner leak", items, err)
		}
		receipts, err := service.IntakeHistory(tc.ctx, "", 20, 0)
		if err != nil || receipts.Total != 1 {
			t.Fatal("RSS intake leak", receipts, err)
		}
		changes, err := service.Changes(tc.ctx, 0, 100, nil)
		if err != nil || changes.HighWatermark != 1 || len(changes.Changes) != 1 {
			t.Fatal("RSS cursor leak", changes, err)
		}
	}
	if _, err = worker.Once(ac, bs.ID, true); err != study.ErrNotFound {
		t.Fatal("foreign RSS source reachable", err)
	}
	no := false
	_, err = service.UpdateUser(ctx, a.ID, a.Revision, study.UserInput{Email: a.Email, DisplayName: a.DisplayName, Enabled: &no})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Once(ac, as.ID, true); err != study.ErrUnauthorized {
		t.Fatal("disabled owner polled", err)
	}
}
