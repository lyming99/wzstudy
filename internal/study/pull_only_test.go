package study

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRetiredPushRecordsRemainInertAcrossFileRestart(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "workspace.json")
	store, err := OpenFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store)
	_, key, err := s.CreateClient(ctx, ClientInput{Slug: "learning-client", Name: "学习端", Permissions: []string{PermissionRead, PermissionSync}})
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := clientSource(t, s)
	item := approveManaged(t, s, managedArticle(t, s, "pending", "保留的已发布资料"))
	changes, err := s.Changes(ctx, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an existing workspace without rewriting its historical schema.
	err = store.Update(ctx, func(tx Tx) error {
		legacy, err := tx.Source(legacy.ID)
		if err != nil {
			return err
		}
		legacy.Permissions = []string{PermissionIngest, PermissionUpload, "inbox"}
		if err = tx.SaveSource(legacy); err != nil {
			return err
		}
		if err = tx.SaveRecord("plan", "old-due-plan", json.RawMessage(`{"id":"old-due-plan","status":"scheduled","scheduled_at":"2020-01-01T00:00:00Z"}`)); err != nil {
			return err
		}
		// Even records that no longer match any live model must be ignored.
		return tx.SaveRecord("delivery", "old-delivery", json.RawMessage(`{"legacy":true}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = NewService(reopened)
	workspace, err := s.Workspace(ctx)
	if err != nil || workspace.Summary.Published != 1 || workspace.Recent[0].ID != item.ID {
		t.Fatal("legacy records broke the material workspace", workspace, err)
	}
	raw, err := json.Marshal(workspace)
	if err != nil || strings.Contains(string(raw), `"plans"`) || strings.Contains(string(raw), `"inbox"`) {
		t.Fatal("retired module leaked into workspace response", string(raw), err)
	}
	got, err := s.Source(ctx, legacy.ID)
	if err != nil || !reflect.DeepEqual(got.Permissions, []string{PermissionIngest, PermissionUpload}) || got.Allows("inbox") {
		t.Fatal("retired grant remained active", got.Permissions, err)
	}
	if _, err = s.ClientForKey(ctx, key, PermissionRead); err != nil {
		t.Fatal("existing reading key stopped working", err)
	}
	if _, err = s.ClientForKey(ctx, key, "inbox"); err != ErrForbidden {
		t.Fatal("retired grant could still authorize", err)
	}
	page, err := s.Changes(ctx, changes.NextCursor, 100, nil)
	if err != nil || len(page.Changes) != 0 || page.NextCursor != changes.NextCursor {
		t.Fatal("legacy plans produced extra events or reset cursor", page, err)
	}
	after, err := os.ReadFile(file)
	if err != nil || string(before) != string(after) {
		t.Fatal("loading and querying rewrote legacy workspace", err)
	}
}

func TestInboxOnlyLegacyPolicyDoesNotGainPermissions(t *testing.T) {
	source := Source{Permissions: []string{"inbox"}}
	if got := source.EffectivePermissions(); got == nil || len(got) != 0 {
		t.Fatal("retired-only policy must become explicit empty, not legacy defaults", got)
	}
	for _, permission := range []string{PermissionRead, PermissionSync, PermissionIngest, PermissionUpload, "inbox"} {
		if source.Allows(permission) {
			t.Fatal("retired-only policy gained permission", permission)
		}
	}
}
