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

func permissionInput(source Source, permissions []string) SourceInput {
	return SourceInput{Slug: source.Slug, Name: source.Name, Description: source.Description, Kind: source.Kind, Integration: source.Integration, Category: source.Category, Language: source.Language, AutoPublish: source.AutoPublish, PollIntervalSeconds: source.PollIntervalSeconds, Permissions: permissions}
}

func TestNormalizeClientPermissions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want []string
		fail     bool
	}{
		{"legacy", nil, nil, false}, {"none", []string{}, []string{}, false},
		{"canonical", []string{"upload", "ingest"}, []string{"ingest", "upload"}, false},
		{"query-denied", []string{"read"}, nil, true}, {"retired-inbox", []string{"inbox"}, nil, true},
		{"unknown", []string{"admin"}, nil, true}, {"duplicate", []string{"ingest", "ingest"}, nil, true},
		{"case-sensitive", []string{"INGEST"}, nil, true}, {"no-trimming", []string{" ingest"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := normalizePermissions(tc.in)
			if (e != nil) != tc.fail || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, e, tc.want)
			}
		})
	}
	s := NewService(NewMemoryStore())
	if _, _, e := s.CreateSource(context.Background(), SourceInput{Slug: "rss", Name: "RSS", Kind: "rss", FeedURL: "https://example.com/feed", Permissions: []string{PermissionRead}}); e != ErrInvalid {
		t.Fatal(e)
	}
	for _, p := range [][]string{{PermissionIngest}, {PermissionUpload}, {PermissionRead, PermissionUpload}, {"inbox"}} {
		if _, _, e := s.CreateClient(context.Background(), ClientInput{Slug: "query", Name: "Query", Permissions: p}); e != ErrInvalid {
			t.Fatal("client accepted writing/retired grant", e)
		}
	}
}

func TestClientPoliciesLegacyExplicitEmptyAndUpdates(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	legacy, legacyKey := clientSource(t, s)
	if !legacy.Allows(PermissionIngest) || !legacy.Allows(PermissionUpload) || legacy.Allows(PermissionRead) || legacy.Allows(PermissionSync) {
		t.Fatal(legacy.EffectivePermissions())
	}
	reader, key, e := s.CreateClient(ctx, ClientInput{Slug: "reader", Name: "Reader", Permissions: []string{PermissionRead}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClientForKey(ctx, key, PermissionRead); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClientForKey(ctx, legacyKey, PermissionRead); e != ErrUnauthorized {
		t.Fatal("source key entered query domain", e)
	}
	if _, e = s.Ingest(ctx, legacy.ID, key, []Content{{ExternalID: "x", Title: "denied"}}); e != ErrUnauthorized {
		t.Fatal("query key ingested", e)
	}
	reader, e = s.UpdateClient(ctx, reader.ID, reader.Revision, ClientInput{Slug: reader.Slug, Name: reader.Name})
	if e != nil || !reader.Allows(PermissionRead) {
		t.Fatal(reader, e)
	}
	reader, e = s.UpdateClient(ctx, reader.ID, reader.Revision, ClientInput{Slug: reader.Slug, Name: reader.Name, Permissions: []string{}})
	if e != nil || reader.Permissions == nil || len(reader.Permissions) != 0 {
		t.Fatal(reader, e)
	}
	reader, e = s.UpdateClient(ctx, reader.ID, reader.Revision, ClientInput{Slug: reader.Slug, Name: reader.Name})
	if e != nil || reader.Permissions == nil || len(reader.Permissions) != 0 {
		t.Fatal("omitted restored revoked policy", reader, e)
	}
	if _, e = s.ClientForKey(ctx, key, PermissionRead); e != ErrForbidden {
		t.Fatal(e)
	}
	reader, e = s.UpdateClient(ctx, reader.ID, reader.Revision, ClientInput{Slug: reader.Slug, Name: reader.Name, Permissions: []string{PermissionRead}})
	if e != nil {
		t.Fatal(e)
	}
	reader, newKey, e := s.RotateClientKey(ctx, reader.ID, reader.Revision)
	if e != nil || newKey == key {
		t.Fatal(e)
	}
	if _, e = s.ClientForKey(ctx, key, PermissionRead); e != ErrUnauthorized {
		t.Fatal(e)
	}
	if _, e = s.ClientForKey(ctx, newKey, PermissionRead); e != nil {
		t.Fatal(e)
	}
	enabled := false
	if _, e = s.UpdateClient(ctx, reader.ID, reader.Revision, ClientInput{Slug: reader.Slug, Name: reader.Name, Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClientForKey(ctx, newKey, PermissionRead); e != ErrDisabled {
		t.Fatal(e)
	}
}

func TestClientPermissionsPersistAndDoNotExposeSecrets(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.json")
	store, e := OpenFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(store)
	none, noneKey, e := s.CreateClient(ctx, ClientInput{Slug: "none", Name: "None", Permissions: []string{}})
	if e != nil {
		t.Fatal(e)
	}
	reader, key, e := s.CreateClient(ctx, ClientInput{Slug: "reader", Name: "Reader"})
	if e != nil {
		t.Fatal(e)
	}
	legacy, sourceKey := clientSource(t, s)
	store.Close()
	raw, e := os.ReadFile(path)
	if e != nil || strings.Contains(string(raw), key) || strings.Contains(string(raw), sourceKey) || strings.Contains(string(raw), noneKey) {
		t.Fatal("plaintext persisted", e)
	}
	store, e = OpenFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	s = NewService(store)
	none, e = s.Client(ctx, none.ID)
	if e != nil || none.Permissions == nil || len(none.Permissions) != 0 {
		t.Fatal(none, e)
	}
	if _, e = s.ClientForKey(ctx, key, PermissionSync); e != nil {
		t.Fatal(e)
	}
	legacy, e = s.Source(ctx, legacy.ID)
	if e != nil || legacy.Permissions != nil || !legacy.Allows(PermissionIngest) {
		t.Fatal(legacy, e)
	}
	reader, e = s.Client(ctx, reader.ID)
	dto, me := json.Marshal(reader)
	if e != nil || me != nil || strings.Contains(string(dto), key) || strings.Contains(string(dto), HashKey(key)) {
		t.Fatal("public record leaked hash", e, me)
	}
}

func TestClientPermissionSlicesAreTransactionIsolated(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	in := SourceInput{Slug: "source", Name: "Source", Permissions: []string{PermissionIngest}}
	v, _, e := s.CreateSource(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	in.Permissions[0] = PermissionUpload
	v.Permissions[0] = PermissionUpload
	got, _ := s.Source(ctx, v.ID)
	got.Permissions[0] = PermissionUpload
	if e = s.Store.Update(ctx, func(tx Tx) error {
		v, _ := tx.Source(got.ID)
		v.Permissions[0] = PermissionUpload
		_ = tx.SaveSource(v)
		return ErrConflict
	}); e != ErrConflict {
		t.Fatal(e)
	}
	got, e = s.Source(ctx, v.ID)
	if e != nil || !reflect.DeepEqual(got.Permissions, []string{PermissionIngest}) {
		t.Fatal(got, e)
	}
	client, _, e := s.CreateClient(ctx, ClientInput{Slug: "client", Name: "Client"})
	if e != nil {
		t.Fatal(e)
	}
	client.Permissions[0] = "admin"
	actual, _ := s.Client(ctx, client.ID)
	if actual.Permissions[0] != PermissionRead {
		t.Fatal("client response alias mutated storage")
	}
}
