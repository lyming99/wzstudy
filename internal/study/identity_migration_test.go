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

func seedLegacyIdentity(t *testing.T, s *Service, slug string, permissions []string) (Source, string) {
	t.Helper()
	ctx := context.Background()
	v, key, e := s.CreateSource(ctx, SourceInput{Slug: slug, Name: slug})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Store.Update(ctx, func(tx Tx) error {
		raw, e := tx.Source(v.ID)
		if e != nil {
			return e
		}
		raw.Permissions = permissions
		v = raw
		return tx.SaveSource(raw)
	})
	if e != nil {
		t.Fatal(e)
	}
	return v, key
}

func TestIdentityMigrationIsExplicitAtomicAndDoesNotChangeMaterials(t *testing.T) {
	for _, mode := range []string{"memory", "file"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var store Store = NewMemoryStore()
			path := ""
			if mode == "file" {
				path = filepath.Join(t.TempDir(), "workspace.json")
				fs, e := OpenFileStore(path)
				if e != nil {
					t.Fatal(e)
				}
				store = fs
			}
			defer store.Close()
			s := NewService(store)
			reader, readKey := seedLegacyIdentity(t, s, "old-reader", []string{PermissionRead, PermissionSync, "inbox"})
			mixed, mixedKey := seedLegacyIdentity(t, s, "old-mixed", []string{PermissionRead, PermissionSync, PermissionIngest, PermissionUpload})
			plain, plainKey := seedLegacyIdentity(t, s, "old-collector", nil)
			item := approveManaged(t, s, managedArticle(t, s, "pending", "material must survive"))
			clock, e := s.Changes(ctx, 0, 100, nil)
			if e != nil {
				t.Fatal(e)
			}
			var before []byte
			if path != "" {
				before, _ = os.ReadFile(path)
			}
			preview, e := s.PreviewIdentityMigration(ctx)
			if e != nil || preview.Pending != 2 || preview.Mixed != 1 {
				t.Fatal(preview, e)
			}
			if path != "" {
				after, _ := os.ReadFile(path)
				if string(after) != string(before) {
					t.Fatal("preflight wrote workspace")
				}
			}
			if _, e = s.ClientForKey(ctx, readKey, PermissionRead); e != ErrUnauthorized {
				t.Fatal("unmigrated source queried", e)
			}
			if out, e := s.MigrateIdentities(ctx, nil); e != ErrConflict || out.Converted != 0 || len(out.NewKeys) != 0 {
				t.Fatal("mixed policy migrated without choice", out, e)
			}
			clients, _ := s.Clients(ctx)
			if len(clients) != 0 {
				t.Fatal("partial read-only migration committed")
			}
			if _, e = s.MigrateIdentities(ctx, []IdentityMigrationChoice{{SourceID: mixed.ID, Revision: mixed.Revision - 1, KeepKey: "ingest"}}); e != ErrConflict {
				t.Fatal("stale choice accepted", e)
			}
			out, e := s.MigrateIdentities(ctx, []IdentityMigrationChoice{{SourceID: mixed.ID, Revision: mixed.Revision, KeepKey: "ingest"}})
			if e != nil || out.Converted != 2 || len(out.NewKeys) != 1 || out.NewKeys[0].IdentityType != "client" {
				t.Fatal(out, e)
			}
			if _, e = s.ClientForKey(ctx, readKey, PermissionRead); e != nil {
				t.Fatal("readonly key not preserved", e)
			}
			if _, e = s.AuthorizeSource(ctx, reader.ID, readKey, PermissionIngest); e != ErrUnauthorized {
				t.Fatal("query key duplicated on source", e)
			}
			if _, e = s.AuthorizeSource(ctx, mixed.ID, mixedKey, PermissionIngest); e != nil {
				t.Fatal("ingest owner lost old key", e)
			}
			if _, e = s.ClientForKey(ctx, mixedKey, PermissionRead); e != ErrUnauthorized {
				t.Fatal("same key retained both roles", e)
			}
			if _, e = s.ClientForKey(ctx, out.NewKeys[0].AccessKey, PermissionRead); e != nil {
				t.Fatal(e)
			}
			if _, e = s.AuthorizeSource(ctx, plain.ID, plainKey, PermissionUpload); e != nil {
				t.Fatal("nil legacy policy changed", e)
			}
			got, _ := s.Item(ctx, item.ID)
			afterClock, _ := s.Changes(ctx, clock.NextCursor, 100, nil)
			if got.Revision != item.Revision || got.SourceID != item.SourceID || afterClock.NextCursor != clock.NextCursor || len(afterClock.Changes) != 0 {
				t.Fatal("migration changed materials/cursor")
			}
			again, e := s.MigrateIdentities(ctx, nil)
			if e != nil || again.Converted != 0 {
				t.Fatal("migration not idempotent", again, e)
			}
			if path != "" {
				raw, _ := os.ReadFile(path)
				for _, key := range []string{readKey, mixedKey, plainKey, out.NewKeys[0].AccessKey} {
					if strings.Contains(string(raw), key) {
						t.Fatal("plaintext persisted")
					}
				}
				reopened, e := OpenFileStore(path)
				if e != nil {
					t.Fatal(e)
				}
				defer reopened.Close()
				s = NewService(reopened)
				if _, e = s.ClientForKey(ctx, readKey, PermissionSync); e != nil {
					t.Fatal("migrated key lost after restart", e)
				}
				if _, e = s.ClientForKey(ctx, out.NewKeys[0].AccessKey, PermissionRead); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestMixedIdentityCanKeepQueryKeyWithoutDuplicatingCredentials(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	mixed, key := seedLegacyIdentity(t, s, "mixed", []string{PermissionRead, PermissionIngest, PermissionUpload})
	enabled := false
	if e := s.Store.Update(ctx, func(tx Tx) error { v, _ := tx.Source(mixed.ID); v.Enabled = enabled; return tx.SaveSource(v) }); e != nil {
		t.Fatal(e)
	}
	out, e := s.MigrateIdentities(ctx, []IdentityMigrationChoice{{SourceID: mixed.ID, Revision: mixed.Revision, KeepKey: "query"}})
	if e != nil || len(out.NewKeys) != 1 || out.NewKeys[0].IdentityType != "source" {
		t.Fatal(out, e)
	}
	if _, e = s.ClientForKey(ctx, key, PermissionRead); e != ErrDisabled {
		t.Fatal("disabled identity was enabled", e)
	}
	if _, e = s.AuthorizeSource(ctx, mixed.ID, key, PermissionIngest); e != ErrUnauthorized {
		t.Fatal("old key still writes", e)
	}
	if _, e = s.AuthorizeSource(ctx, mixed.ID, out.NewKeys[0].AccessKey, PermissionIngest); e != ErrDisabled {
		t.Fatal(e)
	}
	v, _ := s.Source(ctx, mixed.ID)
	if !reflect.DeepEqual(v.Permissions, []string{PermissionIngest, PermissionUpload}) {
		t.Fatal(v.Permissions)
	}
	clients, _ := s.Clients(ctx)
	if len(clients) != 1 || !reflect.DeepEqual(clients[0].Permissions, []string{PermissionRead}) {
		t.Fatal(clients)
	}
	serialized, _ := json.Marshal(clients)
	if strings.Contains(string(serialized), key) || strings.Contains(string(serialized), HashKey(key)) {
		t.Fatal("response leaked credential")
	}
}

func TestIntakeReceiptsAreAtomicAndContainOnlyCommittedIDs(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	source, key := clientSource(t, s)
	bad, e := s.Ingest(ctx, source.ID, key, []Content{{ExternalID: "ok", Title: "ok"}, {ExternalID: "bad", Title: ""}})
	if e != ErrInvalid || bad.ReceiptID != "" {
		t.Fatal(bad, e)
	}
	log, _ := s.IntakeHistory(ctx, "", 20, 0)
	if log.Total != 0 {
		t.Fatal("failed batch logged success")
	}
	out, e := s.Ingest(ctx, source.ID, key, []Content{{ExternalID: "ok", Title: "private title", Content: "private body"}})
	if e != nil || out.ReceiptID == "" || out.SourceID != source.ID || out.Created != 1 {
		t.Fatal(out, e)
	}
	again, e := s.Ingest(ctx, source.ID, key, []Content{{ExternalID: "ok", Title: "private title", Content: "private body"}})
	if e != nil || again.Unchanged != 1 || again.Items[0].ID != out.Items[0].ID {
		t.Fatal(again, e)
	}
	log, e = s.IntakeHistory(ctx, source.ID, 1, 0)
	raw, _ := json.Marshal(log)
	if e != nil || log.Total != 2 || len(log.Items) != 1 || strings.Contains(string(raw), "private title") || strings.Contains(string(raw), "private body") {
		t.Fatal(log, e)
	}
	if _, e = s.IntakeHistory(ctx, "", 101, 0); e != ErrInvalid {
		t.Fatal(e)
	}
}
