package study

import (
	"context"
	"testing"
	"time"
)

func TestPostgresCommitVisibility(t *testing.T) {
	s := fixture(t, "postgres")
	source, key := newSource(t, s, "visibility", true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	written := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	go func() {
		finished <- s.Store.Update(ctx, func(tx Tx) error {
			if _, e := s.ingest(tx, source, []Content{content("first", "first")}); e != nil {
				return e
			}
			close(written)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-written:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// PostgreSQL MVCC must expose neither the uncommitted event nor its clock.
	p, e := s.Changes(ctx, 0, 100, nil)
	if e != nil || p.HighWatermark != 0 || len(p.Changes) != 0 {
		t.Fatal("uncommitted publication leaked", p, e)
	}
	second := make(chan error, 1)
	go func() { _, err := s.Ingest(ctx, source.ID, key, []Content{content("second", "second")}); second <- err }()
	close(release)
	if e = <-finished; e != nil {
		t.Fatal(e)
	}
	if e = <-second; e != nil {
		t.Fatal(e)
	}
	p = page(t, s, 0, 100)
	if len(p.Changes) != 2 || p.Changes[0].Item.ExternalID != "first" || p.Changes[1].Item.ExternalID != "second" || p.NextCursor != 2 {
		t.Fatal("commit order lost", p)
	}
}
func TestMemoryOutboxSnapshotsAreDetached(t *testing.T) {
	s := fixture(t, "memory")
	v, key := newSource(t, s, "detached", true)
	item := ingestOne(t, s, v, key, "id", "original")
	item.Published.Title = "caller mutation"
	item.Draft.Tags[0] = "changed"
	p := page(t, s, 0, 100)
	if p.Changes[0].Item.Title != "original" {
		t.Fatal("caller mutated persisted event")
	}
	p.Changes[0].Item.Title = "reader mutation"
	if p = page(t, s, 0, 100); p.Changes[0].Item.Title != "original" {
		t.Fatal("reader mutated persisted event")
	}
	stored, e := s.Item(context.Background(), item.ID)
	if e != nil || stored.Draft.Tags[0] != "A" {
		t.Fatal(stored, e)
	}
}
