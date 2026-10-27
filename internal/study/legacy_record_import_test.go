package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLegacyRecordImportPreservesTimesAndIsAtomic(t *testing.T) {
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
			item := approveManaged(t, s, managedArticle(t, s, "pending", "待导入资料"))
			created := now.Add(-24 * time.Hour)
			updated := now.Add(-12 * time.Hour)
			root := LegacyRecordInput{EventID: uuid.NewString(), ItemID: item.ID, RecordID: uuid.NewString(), Text: "历史学习记录", CreatedAt: &created, UpdatedAt: &updated, ItemRevision: item.Published.Revision}
			child := LegacyRecordInput{EventID: uuid.NewString(), ItemID: item.ID, RecordID: uuid.NewString(), ParentID: root.RecordID, Text: "历史回复", CreatedAt: &created, UpdatedAt: &updated, ItemRevision: item.Published.Revision}
			out, err := s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{root, child})
			if err != nil || out.Accepted != 2 {
				t.Fatal(out, err)
			}
			page, err := s.ClientLearningRecords(ctx, key, "", item.ID, 100, 0)
			if err != nil || len(page.Items) != 2 {
				t.Fatal(page, err)
			}
			for _, record := range page.Items {
				if !record.CreatedAt.Equal(created) || record.UpdatedAt == nil || !record.UpdatedAt.Equal(updated) {
					t.Fatal("timestamps not preserved", record)
				}
			}
			again, err := s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{root, child})
			if err != nil || again.Duplicates != 2 || again.Accepted != 0 {
				t.Fatal(again, err)
			}
			changed := root
			otherUpdate := updated.Add(time.Second)
			changed.UpdatedAt = &otherUpdate
			if _, err = s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{changed}); !errors.Is(err, ErrConflict) {
				t.Fatal("changed payload reused event", err)
			}
			if out, err = s.ImportLegacyRecords(ctx, other, []LegacyRecordInput{root}); err != nil || out.Accepted != 1 {
				t.Fatal("client isolation", out, err)
			}
			bad := root
			bad.EventID = uuid.NewString()
			bad.RecordID = uuid.NewString()
			bad.ParentID = uuid.NewString()
			good := root
			good.EventID = uuid.NewString()
			good.RecordID = uuid.NewString()
			if _, err = s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{good, bad}); !errors.Is(err, ErrInvalid) {
				t.Fatal("missing parent", err)
			}
			after, err := s.ClientLearningRecords(ctx, key, "", item.ID, 100, 0)
			if err != nil || len(after.Items) != 2 {
				t.Fatal("partial batch", after, err)
			}
			mismatch := root
			mismatch.EventID = uuid.NewString()
			mismatch.RecordID = uuid.NewString()
			mismatch.ItemRevision++
			if _, err = s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{mismatch}); !errors.Is(err, ErrConflict) {
				t.Fatal("stale version", err)
			}
			missing := root
			missing.UpdatedAt = nil
			if _, err = s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{missing}); !errors.Is(err, ErrInvalid) {
				t.Fatal("missing timestamp", err)
			}
			collision := root
			collision.EventID = uuid.NewString()
			if _, err = s.ImportLegacyRecords(ctx, key, []LegacyRecordInput{collision}); !errors.Is(err, ErrConflict) {
				t.Fatal("record id already exists", err)
			}
		})
	}
}
