package study

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestCatalogStoreContract(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var store Store = NewMemoryStore()
			var path string
			if kind == "file" {
				path = filepath.Join(t.TempDir(), "workspace.json")
				var err error
				store, err = OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			s := NewService(store)
			item := approveManaged(t, s, managedArticle(t, s, "pending", "published"))
			if err := store.Update(ctx, func(tx Tx) error { return tx.SaveRecord("collection", "legacy", []byte(`{"name":"legacy"}`)) }); err != nil {
				t.Fatal(err)
			}
			before, err := s.Changes(ctx, 0, 100, nil)
			if err != nil {
				t.Fatal(err)
			}
			const n = 8
			var wg sync.WaitGroup
			errs := make(chan error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					collection, err := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "集合", Chapters: []CatalogChapter{{Title: "章节", ItemID: item.ID}}})
					if err == nil {
						_, err = s.PublishCatalogCollection(ctx, collection.ID, collection.Revision, true)
					}
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			all, err := s.CatalogChanges(ctx, 0, 20)
			if err != nil || len(all.Changes) != n || all.HighWatermark != n {
				t.Fatalf("changes: %+v %v", all, err)
			}
			for i, change := range all.Changes {
				if change.Cursor != int64(i+1) {
					t.Fatalf("out of order: %+v", all)
				}
			}
			page, err := s.CatalogChanges(ctx, 0, 1)
			if err != nil || page.NextCursor != 1 || !page.HasMore {
				t.Fatalf("page: %+v %v", page, err)
			}
			if _, err = s.CatalogChanges(ctx, n+1, 1); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted future cursor: %v", err)
			}
			after, err := s.Changes(ctx, 0, 100, nil)
			if err != nil || after.HighWatermark != before.HighWatermark {
				t.Fatalf("legacy cursor changed: %+v %+v %v", before, after, err)
			}
			if err = store.View(ctx, func(tx Tx) error {
				raw, e := tx.Record("collection", "legacy")
				if e != nil {
					return e
				}
				if string(raw) != `{"name":"legacy"}` {
					t.Errorf("legacy changed: %s", raw)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				reopened, err := OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				recovered, err := NewService(reopened).CatalogChanges(ctx, 0, 20)
				if err != nil || len(recovered.Changes) != n || recovered.HighWatermark != n {
					t.Fatalf("file recovery: %+v %v", recovered, err)
				}
			}
		})
	}
}
