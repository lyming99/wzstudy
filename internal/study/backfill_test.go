package study

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Sources created before the integration marker existed store an empty value.
// They are still push sources and their queued material must be released too.
func TestPublishPendingIngestedHandlesLegacySourceWithoutIntegration(t *testing.T) {
	ctx := context.Background()
	s := fixture(t, "memory")
	source, _ := newSource(t, s, "legacy-unmarked", false)
	if e := s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Source(source.ID)
		if e != nil {
			return e
		}
		v.Integration = ""
		return tx.SaveSource(v)
	}); e != nil {
		t.Fatal(e)
	}
	var legacy Item
	if e := s.Store.Update(ctx, func(tx Tx) error {
		now := s.Now()
		legacy = Item{ID: uuid.NewString(), SourceID: source.ID, ExternalID: "legacy-unmarked-1", Status: "pending", Revision: 1, CreatedAt: now, UpdatedAt: now}
		legacy.Draft = Content{ExternalID: "legacy-unmarked-1", Title: "升级前推送资料", Content: "正文", Format: "text", Category: "Go"}
		return tx.SaveItem(legacy)
	}); e != nil {
		t.Fatal(e)
	}
	published, e := s.PublishPendingIngested(ctx)
	if e != nil || published != 1 {
		t.Fatalf("backfill published=%d err=%v", published, e)
	}
	stored, e := s.Item(ctx, legacy.ID)
	if e != nil || ReviewState(stored) != "published" || stored.Published == nil {
		t.Fatalf("legacy source material was not published: %+v %v", stored, e)
	}
}

// Pushed material queued for review by an earlier build must become public on
// upgrade, while material created inside the admin workspace keeps its review
// state. The backfill must also be idempotent.
func TestPublishPendingIngestedReleasesOnlyPushedMaterial(t *testing.T) {
	ctx := context.Background()
	s := fixture(t, "memory")
	pushed, key := newSource(t, s, "legacy-push", false)
	if pushed.Integration != "collector" {
		t.Fatalf("fixture source is not a push source: %+v", pushed)
	}
	manual := managedArticle(t, s, "pending", "手动录入待审核")
	if manual.Published != nil || ReviewState(manual) != "pending" {
		t.Fatalf("manual workspace item should stay pending: %+v", manual)
	}
	var legacy Item
	if e := s.Store.Update(ctx, func(tx Tx) error {
		now := s.Now()
		legacy = Item{ID: uuid.NewString(), SourceID: pushed.ID, ExternalID: "legacy-1", Status: "pending", Revision: 1, CreatedAt: now, UpdatedAt: now}
		legacy.Draft = Content{ExternalID: "legacy-1", Title: "历史推送资料", Content: "正文", Format: "text", Category: "Go"}
		return tx.SaveItem(legacy)
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Ingest(ctx, pushed.ID, key, []Content{content("fresh", "新推送")}); e != nil {
		t.Fatal(e)
	}
	published, e := s.PublishPendingIngested(ctx)
	if e != nil || published != 1 {
		t.Fatalf("backfill published=%d err=%v", published, e)
	}
	stored, e := s.Item(ctx, legacy.ID)
	if e != nil || ReviewState(stored) != "published" || stored.Published == nil || stored.Published.Title != "历史推送资料" {
		t.Fatalf("legacy pushed item was not published: %+v %v", stored, e)
	}
	rows, e := s.Items(ctx, Filter{Limit: 20})
	if e != nil || len(rows) != 2 {
		t.Fatalf("client query should expose both pushed items: %+v %v", rows, e)
	}
	for _, v := range rows {
		if v.ID == manual.ID {
			t.Fatal("manual pending item leaked to the client query API")
		}
	}
	storedManual, e := s.Item(ctx, manual.ID)
	if e != nil || ReviewState(storedManual) != "pending" {
		t.Fatalf("manual item review state changed: %+v %v", storedManual, e)
	}
	before := page(t, s, 0, 100)
	again, e := s.PublishPendingIngested(ctx)
	if e != nil || again != 0 {
		t.Fatalf("backfill is not idempotent: published=%d err=%v", again, e)
	}
	if after := page(t, s, 0, 100); len(after.Changes) != len(before.Changes) {
		t.Fatal("repeated backfill emitted duplicate change events")
	}
}
