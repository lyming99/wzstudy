package feed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

type invalidFetcher struct{}

func (invalidFetcher) Fetch(context.Context, study.Source) (Result, error) {
	return Result{Items: []study.Content{{ExternalID: "bad", Title: strings.Repeat("x", 501)}}, ETag: "must-not-cache"}, nil
}
func TestInvalidIngestionReleasesPollLease(t *testing.T) {
	ctx := context.Background()
	s := study.NewService(study.NewMemoryStore())
	v, _, e := s.CreateSource(ctx, study.SourceInput{Slug: "invalid", Name: "invalid", Kind: "rss", FeedURL: "https://example.com/feed", AutoPublish: true})
	if e != nil {
		t.Fatal(e)
	}
	w := Worker{Service: s, Fetcher: invalidFetcher{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, e = w.Once(ctx, v.ID, true); !errors.Is(e, study.ErrInvalid) {
		t.Fatal(e)
	}
	v, e = s.Source(ctx, v.ID)
	if e != nil || v.PollToken != "" || v.LastFetchedAt == nil || v.LastError == "" || v.ETag != "" {
		t.Fatal(v, e)
	}
	p, e := s.Changes(ctx, 0, 100, nil)
	if e != nil || p.HighWatermark != 0 {
		t.Fatal(p, e)
	}
	if _, e = s.ClaimPoll(ctx, v.ID, true); e != nil {
		t.Fatal("lease was not released", e)
	}
}
