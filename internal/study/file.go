package study

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// FileStore is a single-process development store. Transactions are persisted
// before becoming visible; production deployments must use PostgreSQL.
type FileStore struct {
	mu    sync.RWMutex
	path  string
	state memoryState
}
type diskState struct {
	Version int                        `json:"version"`
	Sources map[string]sourceRecord    `json:"sources"`
	Items   map[string]Item            `json:"items"`
	Changes []Change                   `json:"changes"`
	Cursor  int64                      `json:"cursor"`
	Records map[string]json.RawMessage `json:"records"`
}

func OpenFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, ErrInvalid
	}
	path, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	s := &FileStore{path: path, state: NewMemoryStore().state}
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	var d diskState
	if e = json.Unmarshal(b, &d); e != nil {
		return nil, e
	}
	if d.Version != 1 || d.Items == nil || d.Sources == nil || d.Records == nil {
		return nil, ErrInvalid
	}
	s.state = memoryState{items: d.Items, sources: map[string]Source{}, records: d.Records, changes: d.Changes, cursor: d.Cursor}
	for id, r := range d.Sources {
		raw, _ := json.Marshal(r)
		v, e := decodeSource(raw)
		if e != nil {
			return nil, e
		}
		s.state.sources[id] = v
	}
	return s, nil
}
func (s *FileStore) Close() {}
func (s *FileStore) Ping(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ctx.Err()
}
func (s *FileStore) View(ctx context.Context, fn func(Tx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&memoryTx{state: &s.state})
}
func (s *FileStore) Update(ctx context.Context, fn func(Tx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &MemoryStore{state: s.state}
	if e := m.Update(ctx, fn); e != nil {
		return e
	}
	next := m.state
	d := diskState{Version: 1, Sources: map[string]sourceRecord{}, Items: next.items, Records: next.records, Changes: next.changes, Cursor: next.cursor}
	for id, v := range next.sources {
		raw, e := encodeSource(v)
		if e != nil {
			return e
		}
		var r sourceRecord
		if e = json.Unmarshal(raw, &r); e != nil {
			return e
		}
		d.Sources[id] = r
	}
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".workspace-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = os.Rename(name, s.path); e != nil {
		return e
	}
	s.state = next
	return nil
}
