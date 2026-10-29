package study

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type workspaceContextKey struct{}

// WithWorkspace is set by trusted authentication/background-job code, never by
// a caller-supplied owner_id parameter. Empty ownership is legacy/system-only.
func WithWorkspace(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, userID)
}
func WorkspaceUserID(ctx context.Context) string {
	id, _ := ctx.Value(workspaceContextKey{}).(string)
	return id
}

type workspaceStore struct{ Store }

func (s *workspaceStore) View(ctx context.Context, fn func(Tx) error) error {
	return s.Store.View(ctx, func(tx Tx) error { return scopedTransaction(ctx, tx, fn) })
}
func (s *workspaceStore) Update(ctx context.Context, fn func(Tx) error) error {
	return s.Store.Update(ctx, func(tx Tx) error { return scopedTransaction(ctx, tx, fn) })
}
func scopedTransaction(ctx context.Context, tx Tx, fn func(Tx) error) error {
	owner := WorkspaceUserID(ctx)
	if owner == "" {
		return fn(tx)
	}
	user, err := workspaceUserByID(tx, owner)
	if errors.Is(err, ErrNotFound) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if !user.Enabled {
		return ErrUnauthorized
	}
	return fn(&workspaceTx{Tx: tx, owner: owner})
}

type workspaceTx struct {
	Tx
	owner string
}

func globalIdentityKind(kind string) bool {
	return kind == "learning_user" || kind == "learning_user_session" || kind == administratorKind
}
func tenantRecordID(owner, id string) string { return owner + ":" + id }
func recordOwner(raw json.RawMessage) string {
	var marker struct {
		Owner string `json:"_workspace_user_id"`
	}
	_ = json.Unmarshal(raw, &marker)
	return marker.Owner
}
func (t *workspaceTx) Records(kind string) ([]json.RawMessage, error) {
	all, err := t.Tx.Records(kind)
	if err != nil || globalIdentityKind(kind) {
		return all, err
	}
	out := []json.RawMessage{}
	for _, raw := range all {
		if recordOwner(raw) == t.owner {
			out = append(out, raw)
		}
	}
	return out, nil
}
func (t *workspaceTx) Record(kind, id string) (json.RawMessage, error) {
	if globalIdentityKind(kind) {
		return t.Tx.Record(kind, id)
	}
	raw, err := t.Tx.Record(kind, tenantRecordID(t.owner, id))
	if err != nil {
		return nil, err
	}
	if recordOwner(raw) != t.owner {
		return nil, ErrNotFound
	}
	return raw, nil
}
func (t *workspaceTx) SaveRecord(kind, id string, raw json.RawMessage) error {
	if globalIdentityKind(kind) {
		return t.Tx.SaveRecord(kind, id, raw)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil || record == nil {
		return ErrInvalid
	}
	record["_workspace_user_id"], _ = json.Marshal(t.owner)
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return t.Tx.SaveRecord(kind, tenantRecordID(t.owner, id), raw)
}
func (t *workspaceTx) DeleteRecord(kind, id string) error {
	if globalIdentityKind(kind) {
		return t.Tx.DeleteRecord(kind, id)
	}
	return t.Tx.DeleteRecord(kind, tenantRecordID(t.owner, id))
}
func (t *workspaceTx) Sources() ([]Source, error) {
	all, err := t.Tx.Sources()
	if err != nil {
		return nil, err
	}
	out := []Source{}
	for _, source := range all {
		if source.OwnerID == t.owner {
			out = append(out, source)
		}
	}
	return out, nil
}
func (t *workspaceTx) Source(id string) (Source, error) {
	v, err := t.Tx.Source(id)
	if err == nil && v.OwnerID != t.owner {
		return Source{}, ErrNotFound
	}
	return v, err
}
func (t *workspaceTx) SaveSource(v Source) error {
	if v.OwnerID != "" && v.OwnerID != t.owner {
		return ErrNotFound
	}
	if old, err := t.Tx.Source(v.ID); err == nil && old.OwnerID != t.owner {
		return ErrNotFound
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	v.OwnerID = t.owner
	return t.Tx.SaveSource(v)
}
func (t *workspaceTx) Item(id string) (Item, error) {
	v, err := t.Tx.Item(id)
	if err == nil && v.OwnerID != t.owner {
		return Item{}, ErrNotFound
	}
	return v, err
}
func (t *workspaceTx) FindItem(source, external string) (Item, error) {
	if _, err := t.Source(source); err != nil {
		return Item{}, err
	}
	v, err := t.Tx.FindItem(source, external)
	if err == nil && v.OwnerID != t.owner {
		return Item{}, ErrNotFound
	}
	return v, err
}
func (t *workspaceTx) SaveItem(v Item) error {
	if _, err := t.Source(v.SourceID); err != nil {
		return err
	}
	if v.OwnerID != "" && v.OwnerID != t.owner {
		return ErrNotFound
	}
	if old, err := t.Tx.Item(v.ID); err == nil && old.OwnerID != t.owner {
		return ErrNotFound
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	v.OwnerID = t.owner
	return t.Tx.SaveItem(v)
}
func (t *workspaceTx) Items(f Filter) ([]Item, error) { f.OwnerID = t.owner; return t.Tx.Items(f) }

// Each workspace has its own content cursor and tombstones. Another user's
// changes cannot advance this user's cursor or disclose global activity counts.
func (t *workspaceTx) Cursor() (int64, error) {
	var clock struct {
		Cursor int64 `json:"cursor"`
	}
	raw, err := t.Record("workspace_clock", "singleton")
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	err = json.Unmarshal(raw, &clock)
	return clock.Cursor, err
}
func (t *workspaceTx) Append(change Change) (Change, error) {
	if _, err := t.Source(change.SourceID); err != nil {
		return Change{}, err
	}
	cursor, err := t.Cursor()
	if err != nil {
		return Change{}, err
	}
	change.Cursor = cursor + 1
	if err = recordSave(t, "workspace_clock", "singleton", map[string]any{"cursor": change.Cursor}); err != nil {
		return Change{}, err
	}
	if err = recordSave(t, "workspace_change", fmt.Sprintf("%020d", change.Cursor), change); err != nil {
		return Change{}, err
	}
	return change, nil
}
func (t *workspaceTx) Changes(after int64, limit int, ids []string) (ChangePage, error) {
	high, err := t.Cursor()
	page := ChangePage{Changes: []Change{}, NextCursor: after, HighWatermark: high}
	if err != nil {
		return page, err
	}
	if after < 0 || after > high || limit < 1 || limit > 200 {
		return page, ErrInvalid
	}
	all, err := recordsAs[Change](t, "workspace_change")
	if err != nil {
		return page, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Cursor < all[j].Cursor })
	scanned := 0
	for _, change := range all {
		if change.Cursor <= after {
			continue
		}
		page.NextCursor = change.Cursor
		if MatchesSource(change.SourceID, ids) {
			page.Changes = append(page.Changes, change)
		}
		scanned++
		if scanned == limit {
			break
		}
	}
	page.HasMore = page.NextCursor < high
	return page, nil
}

func transactionOwner(tx Tx) string {
	if scoped, ok := tx.(*workspaceTx); ok {
		return scoped.owner
	}
	return ""
}
