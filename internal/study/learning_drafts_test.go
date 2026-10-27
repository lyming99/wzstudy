package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLearningDraftsIsolationCASAndLegacyImport(t *testing.T) {
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
			client, key := feedbackClient(t, s, []string{"read", "feedback"})
			other, otherKey := feedbackClient(t, s, []string{"read", "feedback"})
			_, readKey := feedbackClient(t, s, []string{"read"})
			item := approveManaged(t, s, managedArticle(t, s, "pending", "资料"))
			position := &LearningPosition{Kind: "article", Version: item.Published.Revision, BlockID: "body", Offset: 1}
			old := now.Add(-time.Hour)
			event := uuid.NewString()
			legacy := LegacyDraftInput{EventID: event, ItemID: item.ID, Text: " 私人草稿 ", Position: position, ItemRevision: item.Published.Revision, UpdatedAt: &old}
			if _, err := s.ImportLegacyDraft(ctx, readKey, legacy); !errors.Is(err, ErrForbidden) {
				t.Fatal("permission", err)
			}
			empty, err := s.ReadLearningDraft(ctx, key, item.ID, "")
			if err != nil || empty.Draft != nil || empty.ClientID != client.ID {
				t.Fatal(empty, err)
			}
			ack, err := s.ImportLegacyDraft(ctx, key, legacy)
			if err != nil || ack.Duplicate || ack.Draft.Text != " 私人草稿 " || ack.Draft.Revision != 1 || !ack.Draft.UpdatedAt.Equal(old) {
				t.Fatal(ack, err)
			}
			duplicate, err := s.ImportLegacyDraft(ctx, key, legacy)
			if err != nil || !duplicate.Duplicate {
				t.Fatal(duplicate, err)
			}
			changed := legacy
			changed.Text = "不同草稿"
			if _, err = s.ImportLegacyDraft(ctx, key, changed); !errors.Is(err, ErrConflict) {
				t.Fatal("event conflict", err)
			}
			collision := legacy
			collision.EventID = uuid.NewString()
			if _, err = s.ImportLegacyDraft(ctx, key, collision); !errors.Is(err, ErrConflict) {
				t.Fatal("existing draft overwritten", err)
			}
			cross, err := s.ReadLearningDraft(ctx, otherKey, item.ID, "")
			if err != nil || cross.Draft != nil || cross.ClientID != other.ID {
				t.Fatal("cross client", cross, err)
			}
			write := LearningDraftCommand{EventID: uuid.NewString(), ItemID: item.ID, Text: "新草稿", Position: position, ExpectedRevision: 1}
			next, err := s.ApplyLearningDraft(ctx, key, write)
			if err != nil || next.Draft.Revision != 2 {
				t.Fatal(next, err)
			}
			stale := write
			stale.EventID = uuid.NewString()
			if _, err = s.ApplyLearningDraft(ctx, key, stale); !errors.Is(err, ErrConflict) {
				t.Fatal("stale write", err)
			}
			clear := LearningDraftCommand{EventID: uuid.NewString(), ItemID: item.ID, ExpectedRevision: 2}
			cleared, err := s.ApplyLearningDraft(ctx, key, clear)
			if err != nil || !cleared.Draft.Deleted || cleared.Draft.Revision != 3 {
				t.Fatal(cleared, err)
			}
			read, err := s.ReadLearningDraft(ctx, key, item.ID, "")
			if err != nil || read.Draft == nil || !read.Draft.Deleted || read.Draft.Revision != 3 {
				t.Fatal(read, err)
			}
			collision.EventID = uuid.NewString()
			if _, err = s.ImportLegacyDraft(ctx, key, collision); !errors.Is(err, ErrConflict) {
				t.Fatal("tombstone resurrected", err)
			}
			if _, err = s.ImportLegacyDraft(ctx, otherKey, legacy); err != nil {
				t.Fatal("different client", err)
			}
			root := uuid.NewString()
			_, err = s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: item.ID, Action: "record_create", RecordID: root, Text: "父记录"}})
			if err != nil {
				t.Fatal(err)
			}
			reply := legacy
			reply.EventID = uuid.NewString()
			reply.ParentID = root
			if _, err = s.ImportLegacyDraft(ctx, otherKey, reply); !errors.Is(err, ErrNotFound) {
				t.Fatal("other client's parent", err)
			}
			if _, err = s.ImportLegacyDraft(ctx, key, reply); err != nil {
				t.Fatal("own parent", err)
			}
			invalid := legacy
			invalid.EventID = uuid.NewString()
			invalid.ParentID = uuid.NewString()
			invalid.ItemRevision++
			if _, err = s.ImportLegacyDraft(ctx, key, invalid); !errors.Is(err, ErrConflict) {
				t.Fatal("item revision", err)
			}
		})
	}
}
