package study

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func feedbackBool(v bool) *bool { return &v }
func feedbackClient(t *testing.T, s *Service, permissions []string) (Client, string) {
	t.Helper()
	client, key, e := s.CreateClient(context.Background(), ClientInput{Slug: "f-" + uuid.NewString(), Name: "反馈客户端", Permissions: permissions})
	if e != nil {
		t.Fatal(e)
	}
	return client, key
}
func feedbackInput(item Item, action string, at time.Time) FeedbackInput {
	return FeedbackInput{EventID: uuid.NewString(), ItemID: item.ID, Action: action, OccurredAt: &at}
}

func TestFeedbackStoreContract(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			var store Store = NewMemoryStore()
			if kind == "file" {
				var e error
				store, e = OpenFileStore(filepath.Join(t.TempDir(), "workspace.json"))
				if e != nil {
					t.Fatal(e)
				}
			}
			s := NewService(store)
			ctx := context.Background()
			now := time.Now().UTC()
			s.Now = func() time.Time { return now }
			client, key := feedbackClient(t, s, []string{"read", "sync", "feedback"})
			other, otherKey := feedbackClient(t, s, []string{"read", "sync", "feedback"})
			first := approveManaged(t, s, managedArticle(t, s, "pending", "first"))
			second := approveManaged(t, s, managedArticle(t, s, "pending", "second"))
			private := managedArticle(t, s, "draft", "private")
			before, _ := s.Changes(ctx, 0, 100, nil)
			recommendation := feedbackInput(first, "recommended", now)
			read := feedbackInput(first, "read", now)
			result, e := s.ReportFeedback(ctx, key, []FeedbackInput{recommendation, read})
			if e != nil || result.Accepted != 2 || result.Duplicates != 0 {
				t.Fatal(result, e)
			}
			repeat, e := s.ReportFeedback(ctx, key, []FeedbackInput{recommendation, read})
			if e != nil || repeat.Accepted != 0 || repeat.Duplicates != 2 {
				t.Fatal(repeat, e)
			}
			changed := recommendation
			changed.Action = "like"
			if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{changed}); !errors.Is(e, ErrConflict) {
				t.Fatal("id reused with changed payload", e)
			}
			f := Filter{Limit: 20}
			page, e := s.ClientItems(ctx, key, "", f, []string{"recommended", "read"}, nil, true)
			if e != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID {
				t.Fatal(page, e)
			}
			page, e = s.ClientItems(ctx, otherKey, "", f, []string{"seen"}, nil, true)
			if e != nil || len(page.Items) != 2 {
				t.Fatal("cross-client exclusion", page, e)
			}
			own, e := s.ClientFeedback(ctx, key, "", nil, 100, 0)
			if e != nil || own.ClientID != client.ID || own.Total != 1 || own.Summary["read"] != 1 || own.Summary["recommended"] != 1 || own.EventCounts["read"] != 1 {
				t.Fatal(own, e)
			}
			empty, e := s.ClientFeedback(ctx, otherKey, "", nil, 100, 0)
			if e != nil || empty.ClientID != other.ID || empty.Total != 0 {
				t.Fatal(empty, e)
			}
			// Distinct event IDs count as separate interactions; retries never count twice.
			like := feedbackInput(first, "like", now.Add(-time.Hour))
			unlike := feedbackInput(first, "like", now)
			unlike.Value = feedbackBool(false)
			if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{unlike, like}); e != nil {
				t.Fatal(e)
			}
			state, _ := s.ClientFeedback(ctx, key, "", nil, 100, 0)
			if state.Items[0].Actions["like"].Active || state.Items[0].Actions["like"].Count != 1 {
				t.Fatal("old action overwrote undo", state)
			}
			// Same timestamp follows accepted sequence, including actions with omitted time.
			on := FeedbackInput{EventID: uuid.NewString(), ItemID: second.ID, Action: "favorite"}
			off := on
			off.EventID = uuid.NewString()
			off.Value = feedbackBool(false)
			if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{on, off}); e != nil {
				t.Fatal(e)
			}
			states, _ := s.ClientFeedback(ctx, key, "", []string{second.ID}, 20, 0)
			if states.Items[0].Actions["favorite"].Active {
				t.Fatal("batch order lost")
			}
			bad := feedbackInput(private, "read", now)
			good := feedbackInput(second, "recommended", now)
			if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{good, bad}); !errors.Is(e, ErrNotFound) {
				t.Fatal(e)
			}
			events, _ := s.FeedbackEvents(ctx, key, "", 0, 100)
			if events.HighWatermark != 6 || len(events.Events) != 6 {
				t.Fatal("partial failed batch committed", events)
			}
			eventPage, e := s.FeedbackEvents(ctx, key, "", 0, 2)
			if e != nil || len(eventPage.Events) != 2 || eventPage.NextCursor != 2 || !eventPage.HasMore {
				t.Fatal(eventPage, e)
			}
			since := now.Add(time.Second)
			page, e = s.ClientItems(ctx, key, "", f, []string{"seen"}, &since, false)
			if e != nil || len(page.Items) != 2 {
				t.Fatal("cooldown window", page, e)
			}
			// Exclusion happens before limit/offset; no sparse page from post-filtering.
			page, e = s.ClientItems(ctx, key, "", Filter{Limit: 1}, []string{"recommended"}, nil, false)
			if e != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID {
				t.Fatal(page, e)
			}
			after, _ := s.Changes(ctx, 0, 100, nil)
			if before.HighWatermark != after.HighWatermark || len(before.Changes) != len(after.Changes) {
				t.Fatal("feedback polluted content cursor")
			}
		})
	}
}

func TestFeedbackNotesOrderingAndVisibility(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	client, key := feedbackClient(t, s, []string{"read", "feedback"})
	now := time.Now().UTC()
	item := approveManaged(t, s, managedArticle(t, s, "pending", "published"))
	oldRevision := item.Published.Revision
	item, e := s.SaveManaged(ctx, item.ID, ManagedInput{Revision: item.Revision, Status: "pending", Content: Content{Title: "PRIVATE DRAFT", Content: "secret"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	future := feedbackInput(item, "read", now)
	future.ItemRevision = item.Revision
	if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{future}); !errors.Is(e, ErrInvalid) {
		t.Fatal("draft revision feedback accepted", e)
	}
	progress := 0.75
	record := feedbackInput(item, "record", now)
	record.Note = "学习记录 <script>私人笔记</script>"
	record.Progress = &progress
	record.ItemRevision = oldRevision
	if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{record}); e != nil {
		t.Fatal(e)
	}
	old := record
	old.EventID = uuid.NewString()
	old.Note = "older note"
	at := now.Add(-time.Hour)
	old.OccurredAt = &at
	if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{old}); e != nil {
		t.Fatal(e)
	}
	page, _ := s.ClientFeedback(ctx, key, "", nil, 20, 0)
	if page.Items[0].Actions["record"].Note != record.Note {
		t.Fatal(page)
	}
	admin, e := s.ClientFeedback(ctx, "", client.ID, nil, 20, 0)
	if e != nil || admin.Total != 1 {
		t.Fatal(admin, e)
	}
	item, e = s.Publication(ctx, item.ID, item.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	again := feedbackInput(item, "favorite", now)
	if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{again}); !errors.Is(e, ErrNotFound) {
		t.Fatal("new event on hidden item", e)
	}
	if out, e := s.ReportFeedback(ctx, key, []FeedbackInput{record}); e != nil || out.Duplicates != 1 {
		t.Fatal("accepted event cannot be retried after unpublish", out, e)
	}
}

func TestFeedbackAuthorizationRevocationAndValidation(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	item := approveManaged(t, s, managedArticle(t, s, "pending", "valid"))
	now := time.Now().UTC()
	event := feedbackInput(item, "read", now)
	noGrant, reader := feedbackClient(t, s, []string{"read", "sync"})
	if _, e := s.ReportFeedback(ctx, reader, []FeedbackInput{event}); !errors.Is(e, ErrForbidden) {
		t.Fatal("old client implicitly granted feedback", e)
	}
	_, sourceKey := clientSource(t, s)
	if _, e := s.ReportFeedback(ctx, sourceKey, []FeedbackInput{event}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	client, key := feedbackClient(t, s, []string{"read", "feedback"})
	invalid := []FeedbackInput{event, event, event, event, event, event, event, event}
	invalid[0].Action = "unknown"
	invalid[1].EventID = "bad"
	invalid[2].ItemID = uuid.Nil.String()
	invalid[3].Action = "recommended"
	invalid[3].Value = feedbackBool(false)
	invalid[4].Note = "not allowed"
	invalid[5].ItemRevision = -1
	future := now.Add(time.Hour)
	invalid[6].OccurredAt = &future
	p := 1.1
	invalid[7].Action = "record"
	invalid[7].Progress = &p
	for i, input := range invalid {
		if _, e := s.ReportFeedback(ctx, key, []FeedbackInput{input}); !errors.Is(e, ErrInvalid) {
			t.Fatal(i, e)
		}
	}
	if _, e := s.ReportFeedback(ctx, key, nil); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	disabled := false
	_, e := s.UpdateClient(ctx, client.ID, client.Revision, ClientInput{Slug: client.Slug, Name: client.Name, Enabled: &disabled, Permissions: client.Permissions})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReportFeedback(ctx, key, []FeedbackInput{event}); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	if noGrant.Allows(PermissionFeedback) {
		t.Fatal("default permission changed")
	}
}

func TestFeedbackConcurrentRetryAndFileRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	store, e := OpenFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(store)
	ctx := context.Background()
	client, key := feedbackClient(t, s, []string{"read", "feedback"})
	item := approveManaged(t, s, managedArticle(t, s, "pending", "persist"))
	event := feedbackInput(item, "impression", time.Now().UTC())
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ReportFeedback(ctx, key, []FeedbackInput{event}); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	reopened, e := OpenFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	s = NewService(reopened)
	page, e := s.ClientFeedback(ctx, key, "", nil, 20, 0)
	if e != nil || page.ClientID != client.ID || page.HighWatermark != 1 || page.Items[0].Actions["impression"].Count != 1 {
		t.Fatal(page, e)
	}
	if out, e := s.ReportFeedback(ctx, key, []FeedbackInput{event}); e != nil || out.Duplicates != 1 {
		t.Fatal(out, e)
	}
}

func TestPostgresFeedbackFiltering(t *testing.T) {
	s := fixture(t, "postgres")
	ctx := context.Background()
	if e := s.Store.Ping(ctx); e != nil {
		t.Fatal(e)
	}
	_, key := feedbackClient(t, s, []string{"read", "feedback"})
	_, other := feedbackClient(t, s, []string{"read"})
	first := approveManaged(t, s, managedArticle(t, s, "pending", "pg feedback"))
	second := approveManaged(t, s, managedArticle(t, s, "pending", "pg unseen"))
	now := time.Now().UTC()
	event := feedbackInput(first, "recommended", now)
	if _, e := s.ReportFeedback(ctx, key, []FeedbackInput{event}); e != nil {
		t.Fatal(e)
	}
	f := Filter{Limit: 1}
	page, e := s.ClientItems(ctx, key, "", f, []string{"recommended", "seen"}, nil, true)
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID {
		t.Fatal(page, e)
	}
	cutoff := now.Add(time.Second)
	page, e = s.ClientItems(ctx, key, "", Filter{Limit: 100}, []string{"seen"}, &cutoff, false)
	if e != nil || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	page, e = s.ClientItems(ctx, other, "", Filter{Limit: 100}, []string{"seen"}, nil, false)
	if e != nil || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	if ack, e := s.ReportFeedback(ctx, key, []FeedbackInput{event}); e != nil || ack.Duplicates != 1 {
		t.Fatal(ack, e)
	}
}
