package study

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"
)

type IdentityMigrationEntry struct {
	SourceID          string   `json:"source_id"`
	Name              string   `json:"name"`
	Revision          int64    `json:"revision"`
	Enabled           bool     `json:"enabled"`
	Kind              string   `json:"kind"`
	Ingest            []string `json:"ingest_permissions"`
	Query             []string `json:"query_permissions"`
	RequiresKeyChoice bool     `json:"requires_key_choice"`
}

type IdentityMigrationPreview struct {
	Entries []IdentityMigrationEntry `json:"entries"`
	Pending int                      `json:"pending"`
	Mixed   int                      `json:"mixed"`
}

type IdentityMigrationChoice struct {
	SourceID string `json:"source_id"`
	Revision int64  `json:"revision"`
	KeepKey  string `json:"keep_key"` // ingest or query, never inferred
}

type MigrationNewKey struct {
	IdentityType string `json:"identity_type"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	AccessKey    string `json:"access_key"`
}

type IdentityMigrationResult struct {
	Converted int               `json:"converted"`
	Clients   []Client          `json:"clients"`
	NewKeys   []MigrationNewKey `json:"new_keys"`
}

func legacyPolicy(source Source, order []string) []string {
	out := []string{}
	for _, p := range order {
		if slices.Contains(source.Permissions, p) {
			out = append(out, p)
		}
	}
	return out
}

func identityPreview(tx Tx) (out IdentityMigrationPreview, err error) {
	out.Entries = []IdentityMigrationEntry{}
	sources, err := tx.Sources()
	if err != nil {
		return out, err
	}
	for _, source := range sources {
		if source.System || source.Kind != "manual" {
			continue
		}
		query := legacyPolicy(source, []string{PermissionRead, PermissionSync})
		if len(query) == 0 {
			continue
		}
		ingest := legacyPolicy(source, []string{PermissionIngest, PermissionUpload})
		mixed := len(ingest) > 0
		out.Entries = append(out.Entries, IdentityMigrationEntry{SourceID: source.ID, Name: source.Name, Revision: source.Revision, Enabled: source.Enabled, Kind: source.Kind, Ingest: ingest, Query: query, RequiresKeyChoice: mixed})
		out.Pending++
		if mixed {
			out.Mixed++
		}
	}
	return out, nil
}

func (s *Service) PreviewIdentityMigration(ctx context.Context) (out IdentityMigrationPreview, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { out, err = identityPreview(tx); return err })
	return
}

// MigrateIdentities validates all revisions and every mixed-key choice before
// saving anything. Key ownership is never duplicated across the two domains.
// No startup or GET endpoint runs this migration implicitly.
func (s *Service) MigrateIdentities(ctx context.Context, choices []IdentityMigrationChoice) (out IdentityMigrationResult, err error) {
	if len(choices) > 1000 {
		return out, ErrInvalid
	}
	out.Clients, out.NewKeys = []Client{}, []MigrationNewKey{}
	err = s.Store.Update(ctx, func(tx Tx) error {
		preview, e := identityPreview(tx)
		if e != nil {
			return e
		}
		byID := map[string]IdentityMigrationChoice{}
		for _, choice := range choices {
			if _, ok := byID[choice.SourceID]; ok || (choice.KeepKey != "ingest" && choice.KeepKey != "query") {
				return ErrInvalid
			}
			byID[choice.SourceID] = choice
		}
		for _, entry := range preview.Entries {
			choice, ok := byID[entry.SourceID]
			if entry.RequiresKeyChoice && !ok {
				return ErrConflict
			}
			if ok && (!entry.RequiresKeyChoice || choice.Revision != entry.Revision) {
				return ErrConflict
			}
		}
		for id := range byID {
			found := false
			for _, entry := range preview.Entries {
				found = found || entry.SourceID == id
			}
			if !found {
				return ErrConflict
			}
		}
		for _, entry := range preview.Entries {
			source, e := tx.Source(entry.SourceID)
			if e != nil {
				return e
			}
			now := s.Now()
			clientID := uuid.NewString()
			client := Client{ID: clientID, Slug: "migrated-" + strings.ReplaceAll(clientID, "-", ""), Name: source.Name, Description: source.Description, Enabled: source.Enabled, Permissions: slices.Clone(entry.Query), Revision: 1, CreatedAt: now, UpdatedAt: now, LegacySourceID: source.ID}
			clientHash := source.KeyHash
			if entry.RequiresKeyChoice {
				key, e := NewKey()
				if e != nil {
					return e
				}
				if byID[source.ID].KeepKey == "ingest" {
					key = strings.Replace(key, "wzsk_", "wzck_", 1)
					clientHash = HashKey(key)
					out.NewKeys = append(out.NewKeys, MigrationNewKey{IdentityType: "client", ID: client.ID, Name: client.Name, AccessKey: key})
				} else {
					source.KeyHash = HashKey(key)
					out.NewKeys = append(out.NewKeys, MigrationNewKey{IdentityType: "source", ID: source.ID, Name: source.Name, AccessKey: key})
				}
			} else {
				// Preserve the historical source ID and material provenance, but
				// retire its source credential; the old key belongs only to Client.
				source.KeyHash = ""
			}
			if e = saveClient(tx, client, clientHash); e != nil {
				return e
			}
			source.Permissions = slices.Clone(entry.Ingest)
			source.MigratedClientID = client.ID
			source.Revision++
			source.UpdatedAt = now
			source.PollToken = ""
			if e = tx.SaveSource(source); e != nil {
				return e
			}
			out.Clients = append(out.Clients, cloneClient(client))
			out.Converted++
		}
		return nil
	})
	if err != nil {
		// Never return a key or a success-shaped partial receipt on rollback.
		out = IdentityMigrationResult{}
	}
	return
}
