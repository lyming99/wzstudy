package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecordAndDraftClearAreAtomic(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	_, key := feedbackClient(t, s, []string{"read", "feedback"})
	item := approveManaged(t, s, managedArticle(t, s, "pending", "资料"))
	position := &LearningPosition{Kind: "article", Version: item.Published.Revision, BlockID: "body", Offset: 1}
	draft, err := s.ApplyLearningDraft(ctx, key, LearningDraftCommand{EventID: uuid.NewString(), ItemID: item.ID, Text: " 第一份 ", Position: position})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ApplyLearningDraft(ctx, key, LearningDraftCommand{EventID: uuid.NewString(), ItemID: item.ID, Text: " 第二份 ", Position: position, ExpectedRevision: draft.Draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	stale := draft.Draft.Revision
	record := LearningEventInput{EventID: uuid.NewString(), ItemID: item.ID, Action: "record_create", RecordID: uuid.NewString(), Text: "第二份", Position: position, DraftRevision: &stale}
	if _, err = s.ReportLearning(ctx, key, []LearningEventInput{record}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale draft cleared", err)
	}
	page, err := s.ClientLearningRecords(ctx, key, "", item.ID, 20, 0)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("record partially committed", page, err)
	}
	record.DraftRevision = &second.Draft.Revision
	if _, err = s.ReportLearning(ctx, key, []LearningEventInput{record}); err != nil {
		t.Fatal(err)
	}
	read, err := s.ReadLearningDraft(ctx, key, item.ID, "")
	if err != nil || read.Draft == nil || !read.Draft.Deleted || read.Draft.Revision != 3 {
		t.Fatal(read, err)
	}
	if _, err = s.ApplyLearningDraft(ctx, key, LearningDraftCommand{EventID: uuid.NewString(), ItemID: item.ID, Text: "后续草稿", ExpectedRevision: 3}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.ReportLearning(ctx, key, []LearningEventInput{record})
	if err != nil || retry.Duplicates != 1 {
		t.Fatal(retry, err)
	}
	read, err = s.ReadLearningDraft(ctx, key, item.ID, "")
	if err != nil || read.Draft.Text != "后续草稿" || read.Draft.Deleted {
		t.Fatal("retry deleted a newer draft", read, err)
	}
}

func TestDraftHistoryRetainsClientScopedSnapshotsAfterChange(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	client, key := feedbackClient(t, s, []string{"read", "feedback"})
	_, other := feedbackClient(t, s, []string{"read", "feedback"})
	item := approveManaged(t, s, managedArticle(t, s, "pending", "资料"))
	firstID := uuid.NewString()
	at := time.Now().UTC().Add(-time.Hour)
	legacy := LegacyDraftInput{EventID: firstID, ItemID: item.ID, Text: "旧草稿", ItemRevision: item.Published.Revision, UpdatedAt: &at}
	if _, err := s.ImportLegacyDraft(ctx, key, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyLearningDraft(ctx, key, LearningDraftCommand{EventID: uuid.NewString(), ItemID: item.ID, Text: "新草稿", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	original, err := s.ReadLearningDraftEvent(ctx, key, firstID)
	if err != nil || original.Text != "旧草稿" || original.ClientID != client.ID {
		t.Fatal(original, err)
	}
	if _, err = s.ReadLearningDraftEvent(ctx, other, firstID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-client history", err)
	}
	history, err := s.ListLearningDraftHistory(ctx, key, item.ID, "", 1, 0)
	if err != nil || history.Total != 2 || len(history.Items) != 1 || history.Items[0].Text != "新草稿" {
		t.Fatal(history, err)
	}
	history, err = s.ListLearningDraftHistory(ctx, key, item.ID, "", 1, 1)
	if err != nil || len(history.Items) != 1 || history.Items[0].Text != "旧草稿" {
		t.Fatal(history, err)
	}
	history, err = s.ListLearningDraftHistory(ctx, other, item.ID, "", 100, 0)
	if err != nil || history.Total != 0 {
		t.Fatal(history, err)
	}
}

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
