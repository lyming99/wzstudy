package study

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"wzstudy/migrations"
)

func fixture(t *testing.T, kind string) *Service {
	t.Helper()
	if kind == "memory" {
		return NewService(NewMemoryStore())
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PostgreSQL contract tests require a dedicated test database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "wztest_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); admin.Close() })
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := &PostgresStore{Pool: pool}
	t.Cleanup(store.Close)
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migration is not repeatable: %v", err)
	}
	return NewService(store)
}
func newSource(t *testing.T, s *Service, slug string, auto bool) (Source, string) {
	t.Helper()
	v, key, e := s.CreateSource(context.Background(), SourceInput{Slug: slug, Name: slug, AutoPublish: auto, Category: "Go"})
	if e != nil {
		t.Fatal(e)
	}
	return v, key
}
func content(id, title string) Content {
	return Content{ExternalID: id, Title: title, Content: "正文", Tags: []string{"B", "A"}}
}
func ingestOne(t *testing.T, s *Service, v Source, key, id, title string) Item {
	t.Helper()
	out, e := s.Ingest(context.Background(), v.ID, key, []Content{content(id, title)})
	if e != nil {
		t.Fatal(e)
	}
	return out.Items[0]
}
func page(t *testing.T, s *Service, after int64, limit int, ids ...string) ChangePage {
	t.Helper()
	v, e := s.Changes(context.Background(), after, limit, ids)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestRepositoryContract(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			t.Run("idempotent-and-keys", func(t *testing.T) {
				s := fixture(t, kind)
				v, key := newSource(t, s, "go", true)
				ctx := context.Background()
				input := content("id", "title")
				a, e := s.Ingest(ctx, v.ID, key, []Content{input})
				if e != nil || a.Created != 1 || a.Published != 1 {
					t.Fatalf("%+v %v", a, e)
				}
				input.Tags = []string{"A", "B", "A"}
				b, e := s.Ingest(ctx, v.ID, key, []Content{input})
				if e != nil || b.Unchanged != 1 || b.Items[0].ID != a.Items[0].ID {
					t.Fatalf("retry: %+v %v", b, e)
				}
				if p := page(t, s, 0, 100); len(p.Changes) != 1 || p.NextCursor != 1 {
					t.Fatal(p)
				}
				if _, e = s.Ingest(ctx, v.ID, "wrong", []Content{input}); !errors.Is(e, ErrUnauthorized) {
					t.Fatal(e)
				}
				next, newKey, e := s.RotateKey(ctx, v.ID, v.Revision)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.Ingest(ctx, v.ID, key, []Content{input}); !errors.Is(e, ErrUnauthorized) {
					t.Fatal(e)
				}
				if _, e = s.Ingest(ctx, v.ID, newKey, []Content{input}); e != nil {
					t.Fatal(e)
				}
				raw, _ := json.Marshal(next)
				if strings.Contains(string(raw), HashKey(newKey)) || strings.Contains(string(raw), newKey) {
					t.Fatal("key leaked")
				}
				if _, _, e = s.CreateSource(ctx, SourceInput{Slug: "go", Name: "duplicate"}); !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
			})
			t.Run("ingest-publishes-and-tombstones", func(t *testing.T) {
				s := fixture(t, kind)
				v, key := newSource(t, s, "review", false)
				ctx := context.Background()
				item := ingestOne(t, s, v, key, "id", "draft one")
				if item.Published == nil || item.Published.Title != "draft one" || ReviewState(item) != "published" {
					t.Fatal("pushed material did not become public immediately")
				}
				if len(page(t, s, 0, 100).Changes) != 1 {
					t.Fatal("pushed material did not reach client sync")
				}
				item, e := s.Publication(ctx, item.ID, item.Revision, true)
				if e != nil {
					t.Fatal(e)
				}
				if item.Published.Title != "draft one" {
					t.Fatal(item)
				}
				before := item.Revision
				again, e := s.Publication(ctx, item.ID, before, true)
				if e != nil || again.Revision != before {
					t.Fatal("duplicate approval emitted revision")
				}
				item = ingestOne(t, s, v, key, "id", "draft two")
				if item.Published.Title != "draft two" || len(page(t, s, 0, 100).Changes) != 2 {
					t.Fatal("re-push did not refresh the live snapshot")
				}
				if _, e = s.Publication(ctx, item.ID, before, true); !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
				item, e = s.Publication(ctx, item.ID, item.Revision, true)
				if e != nil {
					t.Fatal(e)
				}
				item, e = s.Publication(ctx, item.ID, item.Revision, false)
				if e != nil {
					t.Fatal(e)
				}
				changes := page(t, s, 0, 100)
				if len(changes.Changes) != 3 || changes.Changes[0].Item.Title != "draft one" || changes.Changes[1].Item.Title != "draft two" || changes.Changes[2].Operation != "delete" || changes.Changes[2].Item != nil {
					t.Fatalf("%+v", changes)
				}
				if _, e = s.Publication(ctx, item.ID, item.Revision, false); e != nil {
					t.Fatal(e)
				}
				if len(page(t, s, 0, 100).Changes) != 3 {
					t.Fatal("duplicate removal emitted event")
				}
			})
			t.Run("batch-atomicity", func(t *testing.T) {
				s := fixture(t, kind)
				v, key := newSource(t, s, "atomic", true)
				ctx := context.Background()
				if _, e := s.Ingest(ctx, v.ID, key, []Content{content("one", "ok"), content("two", "")}); !errors.Is(e, ErrInvalid) {
					t.Fatal(e)
				}
				if _, e := s.Ingest(ctx, v.ID, key, []Content{content("one", "a"), content("one", "b")}); !errors.Is(e, ErrInvalid) {
					t.Fatal(e)
				}
				items, e := s.Items(ctx, Filter{Admin: true, Limit: 100})
				if e != nil || len(items) != 0 || len(page(t, s, 0, 100).Changes) != 0 {
					t.Fatalf("batch left partial data: %v %v", items, e)
				}
				// Inject a failure after a valid write and outbox append: both must roll back.
				e = s.Store.Update(ctx, func(tx Tx) error {
					_, e := s.ingest(tx, v, []Content{content("ok", "ok")})
					if e != nil {
						return e
					}
					return errors.New("injected")
				})
				if e == nil {
					t.Fatal("missing injected failure")
				}
				items, e = s.Items(ctx, Filter{Admin: true, Limit: 100})
				if e != nil || len(items) != 0 || page(t, s, 0, 100).HighWatermark != 0 {
					t.Fatal("rollback failed")
				}
			})
			t.Run("pagination-and-filter-progress", func(t *testing.T) {
				s := fixture(t, kind)
				a, ak := newSource(t, s, "a", true)
				b, bk := newSource(t, s, "b", true)
				ingestOne(t, s, a, ak, "a1", "first")
				ingestOne(t, s, a, ak, "a2", "second")
				ingestOne(t, s, b, bk, "b1", "third")
				p := page(t, s, 0, 2, b.ID)
				if len(p.Changes) != 0 || p.NextCursor != 2 || !p.HasMore || p.HighWatermark != 3 {
					t.Fatal(p)
				}
				p = page(t, s, p.NextCursor, 2, b.ID)
				if len(p.Changes) != 1 || p.NextCursor != 3 || p.HasMore {
					t.Fatal(p)
				}
				if _, e := s.Changes(context.Background(), 4, 100, nil); !errors.Is(e, ErrInvalid) {
					t.Fatal(e)
				}
				rows, e := s.Items(context.Background(), Filter{SourceIDs: []string{b.ID}, Category: "Go", Tag: "A", Query: "THIRD", Limit: 20})
				if e != nil || len(rows) != 1 || rows[0].Published.Title != "third" {
					t.Fatalf("filters: %+v %v", rows, e)
				}
			})
			t.Run("disabled-source-keeps-published-data", func(t *testing.T) {
				s := fixture(t, kind)
				v, key := newSource(t, s, "disable", true)
				ingestOne(t, s, v, key, "a", "a")
				no := false
				_, e := s.UpdateSource(context.Background(), v.ID, v.Revision, SourceInput{Slug: v.Slug, Name: "updated", Kind: v.Kind, Enabled: &no})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.Ingest(context.Background(), v.ID, key, []Content{content("b", "b")}); !errors.Is(e, ErrDisabled) {
					t.Fatal(e)
				}
				rows, e := s.Items(context.Background(), Filter{Limit: 20})
				if e != nil || len(rows) != 1 {
					t.Fatal(rows, e)
				}
			})
			t.Run("concurrent-commit-order", func(t *testing.T) {
				s := fixture(t, kind)
				v, key := newSource(t, s, "concurrent", true)
				var wg sync.WaitGroup
				errs := make(chan error, 24)
				for i := 0; i < 24; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						_, e := s.Ingest(context.Background(), v.ID, key, []Content{content(fmt.Sprint(i), "entry")})
						errs <- e
					}(i)
				}
				wg.Wait()
				close(errs)
				for e := range errs {
					if e != nil {
						t.Fatal(e)
					}
				}
				p := page(t, s, 0, 100)
				if len(p.Changes) != 24 || p.HighWatermark != 24 {
					t.Fatal(p)
				}
				for i, v := range p.Changes {
					if v.Cursor != int64(i+1) || v.Item == nil {
						t.Fatal("cursor gap or partial event")
					}
				}
			})
			t.Run("poll-lease-and-obsolete-results", func(t *testing.T) {
				s := fixture(t, kind)
				ctx := context.Background()
				v, _, e := s.CreateSource(ctx, SourceInput{Slug: "rss", Name: "RSS", Kind: "rss", FeedURL: "https://example.com/feed", AutoPublish: true})
				if e != nil {
					t.Fatal(e)
				}
				job, e := s.ClaimPoll(ctx, v.ID, true)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.ClaimPoll(ctx, v.ID, true); !errors.Is(e, ErrBusy) {
					t.Fatal(e)
				}
				v, e = s.UpdateSource(ctx, v.ID, v.Revision, SourceInput{Slug: v.Slug, Name: v.Name, Kind: v.Kind, FeedURL: "https://example.com/new-feed", AutoPublish: false})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.CompletePoll(ctx, job, []Content{content("stale", "stale")}, false, "tag", "", nil); !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
				job, e = s.ClaimPoll(ctx, v.ID, true)
				if e != nil {
					t.Fatal(e)
				}
				_, e = s.CompletePoll(ctx, job, nil, false, "", "", errors.New("https://sensitive.example?secret=do-not-log"))
				if e != nil {
					t.Fatal(e)
				}
				v, e = s.Source(ctx, v.ID)
				if e != nil || v.LastFetchedAt == nil || v.LastError == "" || strings.Contains(v.LastError, "secret") {
					t.Fatal(v, e)
				}
				if _, e = s.ClaimPoll(ctx, v.ID, false); !errors.Is(e, ErrBusy) {
					t.Fatal(e)
				}
			})
		})
	}
}

func TestInputValidation(t *testing.T) {
	s := fixture(t, "memory")
	v, key := newSource(t, s, "safe", true)
	cases := []Content{content("", "title"), content("id", ""), {ExternalID: "x", Title: "x", Format: "html"}, {ExternalID: "x", Title: "x", URL: "javascript:alert(1)"}, {ExternalID: "x", Title: "x", Tags: []string{strings.Repeat("a", 81)}}, {ExternalID: "x", Title: "x", Content: "\x00"}}
	for _, input := range cases {
		if _, e := s.Ingest(context.Background(), v.ID, key, []Content{input}); !errors.Is(e, ErrInvalid) {
			t.Fatalf("accepted invalid content: %+v %v", input, e)
		}
	}
	if _, _, e := s.CreateSource(context.Background(), SourceInput{Slug: "bad", Name: "bad", Kind: "rss", FeedURL: "https://user:pass@example.com/feed"}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	s.Now = func() time.Time { return now }
	rss, _, e := s.CreateSource(context.Background(), SourceInput{Slug: "expire", Name: "expire", Kind: "rss", FeedURL: "https://example.com/feed"})
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.ClaimPoll(context.Background(), rss.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(91 * time.Second)
	if _, e = s.CompletePoll(context.Background(), job, nil, true, "", "", nil); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
