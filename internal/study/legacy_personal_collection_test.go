package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLegacyPersonalCollectionImport(t *testing.T) {
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
			first := approveManaged(t, s, managedArticle(t, s, "pending", "一"))
			second := approveManaged(t, s, managedArticle(t, s, "pending", "二"))
			unpublished := managedArticle(t, s, "draft", "草稿")
			created, updated := now.Add(-48*time.Hour), now.Add(-24*time.Hour)
			in := LegacyPersonalCollectionInput{EventID: uuid.NewString(), CollectionID: uuid.NewString(), Title: "原始合集", Items: []string{second.ID, first.ID}, CreatedAt: &created, UpdatedAt: &updated}
			if _, err := s.ImportLegacyPersonalCollection(ctx, reader, in); !errors.Is(err, ErrForbidden) {
				t.Fatal("reader may not write", err)
			}
			result, err := s.ImportLegacyPersonalCollection(ctx, key, in)
			if err != nil || result.Duplicate || result.Collection.Revision != 1 {
				t.Fatal(result, err)
			}
			c := result.Collection
			if c.Items[0] != second.ID || c.Items[1] != first.ID || !c.CreatedAt.Equal(created) || !c.UpdatedAt.Equal(updated) {
				t.Fatal("original order or dates changed", c)
			}
			retry, err := s.ImportLegacyPersonalCollection(ctx, key, in)
			if err != nil || !retry.Duplicate {
				t.Fatal(retry, err)
			}
			changed := in
			changed.Title = "其他标题"
			if _, err = s.ImportLegacyPersonalCollection(ctx, key, changed); !errors.Is(err, ErrConflict) {
				t.Fatal("event id collision", err)
			}
			collision := in
			collision.EventID = uuid.NewString()
			if _, err = s.ImportLegacyPersonalCollection(ctx, key, collision); !errors.Is(err, ErrConflict) {
				t.Fatal("collection id collision", err)
			}
			if _, err = s.ImportLegacyPersonalCollection(ctx, other, in); err != nil {
				t.Fatal("client isolation", err)
			}
			invalid := in
			invalid.EventID = uuid.NewString()
			invalid.CollectionID = uuid.NewString()
			invalid.Items = []string{first.ID, unpublished.ID}
			if _, err = s.ImportLegacyPersonalCollection(ctx, key, invalid); !errors.Is(err, ErrNotFound) {
				t.Fatal("unpublished member accepted", err)
			}
			if _, err = s.PersonalCollection(ctx, key, invalid.CollectionID); !errors.Is(err, ErrNotFound) {
				t.Fatal("partial collection created", err)
			}
			invalid.Items = []string{first.ID, first.ID}
			if _, err = s.ImportLegacyPersonalCollection(ctx, key, invalid); !errors.Is(err, ErrInvalid) {
				t.Fatal("duplicate member", err)
			}
			invalid.Items = []string{first.ID}
			invalid.UpdatedAt = nil
			if _, err = s.ImportLegacyPersonalCollection(ctx, key, invalid); !errors.Is(err, ErrInvalid) {
				t.Fatal("missing time", err)
			}
			badTime := created.Add(-time.Second)
			invalid.UpdatedAt = &badTime
			if _, err = s.ImportLegacyPersonalCollection(ctx, key, invalid); !errors.Is(err, ErrInvalid) {
				t.Fatal("time reversed", err)
			}
		})
	}
}
