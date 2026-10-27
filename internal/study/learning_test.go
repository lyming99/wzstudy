package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLearningStoreContract(t *testing.T) {
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
			now := time.Now().UTC().Truncate(time.Millisecond)
			s.Now = func() time.Time { return now }
			client, key := feedbackClient(t, s, []string{"read", "feedback"})
			other, otherKey := feedbackClient(t, s, []string{"read", "feedback"})
			_, reader := feedbackClient(t, s, []string{"read"})
			item := approveManaged(t, s, managedArticle(t, s, "pending", "published"))
			private := managedArticle(t, s, "draft", "private")
			before, _ := s.Changes(ctx, 0, 100, nil)
			boolean := true
			zero := int64(0)
			position := &LearningPosition{Kind: "article", Version: item.Published.Revision, BlockID: "body", Offset: 1}
			recordID := uuid.NewString()
			startA, endA := now.Add(-30*time.Second), now.Add(-20*time.Second)
			startB, endB := now.Add(-25*time.Second), now.Add(-15*time.Second)
			inputs := []LearningEventInput{
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "view"},
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "review_plan", Value: &boolean},
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "review"},
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "progress", Position: position, Mode: "preview", ExpectedRevision: &zero},
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "record_create", RecordID: recordID, Text: "私人的学习记录", Position: position},
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "duration", SessionID: uuid.NewString(), SessionSequence: 1, StartAt: &startA, EndAt: &endA},
				{EventID: uuid.NewString(), ItemID: item.ID, Action: "duration", SessionID: uuid.NewString(), SessionSequence: 1, StartAt: &startB, EndAt: &endB},
			}
			if _, err := s.ReportLearning(ctx, reader, inputs); !errors.Is(err, ErrForbidden) {
				t.Fatalf("read key wrote personal data: %v", err)
			}
			result, err := s.ReportLearning(ctx, key, inputs)
			if err != nil || result.Accepted != len(inputs) || len(result.States) != 1 {
				t.Fatalf("report: %+v %v", result, err)
			}
			state := result.States[0]
			if state.ClientID != client.ID || state.ViewCount != 1 || state.ReviewPending || state.ReviewCount != 1 || state.RecordCount != 1 || state.DurationMS != 15000 || state.Progress["preview"].Revision != 1 || state.Progress["preview"].LastEventID != inputs[3].EventID {
				t.Fatalf("state: %+v", state)
			}
			repeat, err := s.ReportLearning(ctx, key, inputs)
			if err != nil || repeat.Accepted != 0 || repeat.Duplicates != len(inputs) || repeat.States[0].ViewCount != 1 {
				t.Fatalf("idempotency: %+v %v", repeat, err)
			}
			changed := inputs[0]
			changed.Action = "review"
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{changed}); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed event accepted: %v", err)
			}
			stale := inputs[3]
			stale.EventID = uuid.NewString()
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{stale}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale progress accepted: %v", err)
			}
			outOfOrder := inputs[5]
			outOfOrder.EventID = uuid.NewString()
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{outOfOrder}); !errors.Is(err, ErrConflict) {
				t.Fatalf("session sequence reused: %v", err)
			}
			bad := LearningEventInput{EventID: uuid.NewString(), ItemID: private.ID, Action: "view"}
			good := LearningEventInput{EventID: uuid.NewString(), ItemID: item.ID, Action: "view"}
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{good, bad}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("private item accepted: %v", err)
			}
			page, err := s.ClientLearning(ctx, key, "", nil, 20, 0)
			if err != nil || page.HighWatermark != int64(len(inputs)) || page.DurationMS != 15000 || page.Items[0].ViewCount != 1 {
				t.Fatalf("atomicity: %+v %v", page, err)
			}
			windowStart, windowEnd := now.Add(-27*time.Second), now.Add(-18*time.Second)
			window, err := s.ClientLearningPeriod(ctx, key, "", nil, 20, 0, &windowStart, &windowEnd)
			if err != nil || window.DurationMS != 9000 {
				t.Fatalf("bounded duration: %+v %v", window, err)
			}
			if _, err = s.ClientLearningPeriod(ctx, key, "", nil, 20, 0, &windowStart, nil); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unpaired duration window accepted: %v", err)
			}
			otherPage, err := s.ClientLearning(ctx, otherKey, "", nil, 20, 0)
			if err != nil || otherPage.ClientID != other.ID || otherPage.Total != 0 {
				t.Fatalf("identity leaked: %+v %v", otherPage, err)
			}
			records, err := s.ClientLearningRecords(ctx, key, "", item.ID, 20, 0)
			if err != nil || records.Total != 1 || records.Items[0].Text != "私人的学习记录" {
				t.Fatalf("records: %+v %v", records, err)
			}
			otherRecords, err := s.ClientLearningRecords(ctx, otherKey, "", item.ID, 20, 0)
			if err != nil || otherRecords.Total != 0 {
				t.Fatalf("record leaked: %+v %v", otherRecords, err)
			}
			feed, _ := s.Changes(ctx, 0, 100, nil)
			if feed.HighWatermark != before.HighWatermark {
				t.Fatal("learning changed public feed")
			}
			events, err := s.ClientLearningEvents(ctx, key, "", 0, 2)
			if err != nil || len(events.Events) != 2 || events.NextCursor != 2 || !events.HasMore {
				t.Fatalf("events: %+v %v", events, err)
			}
			if kind == "file" {
				reopened, err := OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				recovered, err := NewService(reopened).ClientLearning(ctx, key, "", nil, 20, 0)
				if err != nil || recovered.HighWatermark != int64(len(inputs)) || recovered.Items[0].RecordCount != 1 {
					t.Fatalf("file recovery: %+v %v", recovered, err)
				}
			}
		})
	}
}
