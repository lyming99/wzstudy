package feed

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

// Exercise parsing and worker ingestion without a public network request or
// relaxing the production fetcher's private-address protections.
func TestRSSIntakePublishesImmediatelyAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	service := study.NewService(study.NewMemoryStore())
	source, _, err := service.CreateSource(ctx, study.SourceInput{
		Slug: "rss-intake-review", Name: "学习订阅", Kind: "rss",
		FeedURL: "https://example.org/feed.xml", Category: "学习方法",
		PollIntervalSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &Fetcher{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
			Body: io.NopCloser(strings.NewReader(`<rss version="2.0"><channel><item><guid>intake:lesson-1</guid><title>订阅学习资料</title><link>https://example.org/lesson</link><description>学习摘要</description></item></channel></rss>`)),
		}, nil
	})}}
	worker := Worker{Service: service, Fetcher: fetcher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	first, err := worker.Once(ctx, source.ID, true)
	if err != nil || first.Created != 1 || first.Published != 1 || len(first.Items) != 1 {
		t.Fatalf("first RSS intake: %+v, %v", first, err)
	}
	item, err := service.Item(ctx, first.Items[0].ID)
	if err != nil || item.Status != "published" || item.Published == nil || item.SourceID != source.ID || item.Draft.Category != "学习方法" || item.Draft.URL != "https://example.org/lesson" {
		t.Fatalf("RSS published/source metadata: %+v, %v", item, err)
	}
	second, err := worker.Once(ctx, source.ID, true)
	if err != nil || second.Created != 0 || second.Unchanged != 1 || second.Published != 0 || len(second.Items) != 1 || second.Items[0].ID != item.ID {
		t.Fatalf("RSS replay must reuse the same material: %+v, %v", second, err)
	}
	changes, err := service.Changes(ctx, 0, 100, nil)
	if err != nil || changes.HighWatermark != 1 || len(changes.Changes) != 1 {
		t.Fatalf("collected content missing from public sync: %+v, %v", changes, err)
	}
	updated, err := service.Source(ctx, source.ID)
	if err != nil || updated.LastSuccessAt == nil || updated.PollIntervalSeconds != 600 || updated.AutoPublish {
		t.Fatalf("RSS scheduling/review policy: %+v, %v", updated, err)
	}
}
