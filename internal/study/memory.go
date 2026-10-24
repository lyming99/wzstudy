package study

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

type memoryState struct {
	sources map[string]Source
	items   map[string]Item
	changes []Change
	cursor  int64
	records map[string]json.RawMessage
}
type MemoryStore struct {
	mu    sync.RWMutex
	state memoryState
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: memoryState{sources: map[string]Source{}, items: map[string]Item{}, changes: []Change{}, records: map[string]json.RawMessage{}}}
}
func (s *MemoryStore) Ping(ctx context.Context) error { return ctx.Err() }
func (s *MemoryStore) Close()                         {}
func (s *MemoryStore) View(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&memoryTx{state: &s.state})
}
func (s *MemoryStore) Update(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy-on-write rollback semantics match PostgreSQL, including mid-batch failures.
	copyState := memoryState{sources: map[string]Source{}, items: map[string]Item{}, changes: append([]Change(nil), s.state.changes...), cursor: s.state.cursor, records: map[string]json.RawMessage{}}
	for k, v := range s.state.records {
		copyState.records[k] = append(json.RawMessage(nil), v...)
	}
	for k, v := range s.state.sources {
		copyState.sources[k] = v
	}
	for k, v := range s.state.items {
		copyState.items[k] = cloneItem(v)
	}
	if err := fn(&memoryTx{state: &copyState}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.state = copyState
	return nil
}

type memoryTx struct{ state *memoryState }

func (t *memoryTx) Sources() ([]Source, error) {
	out := make([]Source, 0, len(t.state.sources))
	for _, v := range t.state.sources {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}
func (t *memoryTx) Source(id string) (Source, error) {
	v, ok := t.state.sources[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (t *memoryTx) SaveSource(v Source) error {
	for _, s := range t.state.sources {
		if s.ID != v.ID && s.Slug == v.Slug {
			return ErrConflict
		}
	}
	t.state.sources[v.ID] = v
	return nil
}
func (t *memoryTx) Item(id string) (Item, error) {
	v, ok := t.state.items[id]
	if !ok {
		return v, ErrNotFound
	}
	return cloneItem(v), nil
}
func (t *memoryTx) FindItem(source, external string) (Item, error) {
	for _, v := range t.state.items {
		if v.SourceID == source && v.ExternalID == external {
			return cloneItem(v), nil
		}
	}
	return Item{}, ErrNotFound
}
func (t *memoryTx) SaveItem(v Item) error  { t.state.items[v.ID] = cloneItem(v); return nil }
func (t *memoryTx) Cursor() (int64, error) { return t.state.cursor, nil }
func (t *memoryTx) Append(v Change) (Change, error) {
	t.state.cursor++
	v.Cursor = t.state.cursor
	b, _ := json.Marshal(v)
	var persisted Change
	_ = json.Unmarshal(b, &persisted)
	t.state.changes = append(t.state.changes, persisted)
	return v, nil
}
func (t *memoryTx) Items(f Filter) ([]Item, error) {
	out := make([]Item, 0)
	for _, v := range t.state.items {
		if v.DeletedAt != nil || (f.Admin && f.Status != "" && ReviewState(v) != f.Status) {
			continue
		}
		if !MatchesSource(v.SourceID, f.SourceIDs) {
			continue
		}
		c := v.Draft
		if !f.Admin {
			if v.Published == nil {
				continue
			}
			c = v.Published.Content
		}
		if MatchesContent(c, f) {
			out = append(out, cloneItem(v))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].UpdatedAt, out[j].UpdatedAt
		if !f.Admin {
			a = itemDate(out[i])
			b = itemDate(out[j])
		}
		if a.Equal(b) {
			return out[i].ID > out[j].ID
		}
		if f.Ascending {
			return a.Before(b)
		}
		return a.After(b)
	})
	if f.Offset >= len(out) {
		return []Item{}, nil
	}
	end := min(f.Offset+f.Limit, len(out))
	return out[f.Offset:end], nil
}
func itemDate(v Item) time.Time {
	if v.Published != nil && v.Published.PublishedAt != nil {
		return *v.Published.PublishedAt
	}
	return v.CreatedAt
}
func (t *memoryTx) Changes(after int64, limit int, ids []string) (ChangePage, error) {
	page := ChangePage{Changes: []Change{}, NextCursor: after, HighWatermark: t.state.cursor}
	if after > page.HighWatermark {
		return page, ErrInvalid
	}
	scanned := 0
	for _, v := range t.state.changes {
		if v.Cursor <= after {
			continue
		}
		scanned++
		page.NextCursor = v.Cursor
		if MatchesSource(v.SourceID, ids) {
			b, _ := json.Marshal(v)
			var cloned Change
			_ = json.Unmarshal(b, &cloned)
			page.Changes = append(page.Changes, cloned)
		}
		if scanned >= limit {
			break
		}
	}
	page.HasMore = page.NextCursor < page.HighWatermark
	return page, nil
}
func cloneItem(v Item) Item {
	b, _ := json.Marshal(v)
	var out Item
	_ = json.Unmarshal(b, &out)
	return out
}

func (t *memoryTx) Records(kind string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	keys := []string{}
	for key := range t.state.records {
		if strings.HasPrefix(key, kind+"/") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, append(json.RawMessage(nil), t.state.records[key]...))
	}
	return out, nil
}
func (t *memoryTx) Record(kind, id string) (json.RawMessage, error) {
	v, ok := t.state.records[kind+"/"+id]
	if !ok {
		return nil, ErrNotFound
	}
	return append(json.RawMessage(nil), v...), nil
}
func (t *memoryTx) SaveRecord(kind, id string, b json.RawMessage) error {
	t.state.records[kind+"/"+id] = append(json.RawMessage(nil), b...)
	return nil
}
func (t *memoryTx) DeleteRecord(kind, id string) error {
	delete(t.state.records, kind+"/"+id)
	return nil
}
