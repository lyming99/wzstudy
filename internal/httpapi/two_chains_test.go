package httpapi

import (
	"context"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestIdentityMigrationAPIRequiresExplicitBodyAndAdmin(t *testing.T) {
	s, h, _ := managementApp(t, 1<<20)
	ctx := context.Background()
	source, oldKey, err := s.CreateSource(ctx, study.SourceInput{Slug: "legacy-query", Name: "legacy query"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Update(ctx, func(tx study.Tx) error {
		v, e := tx.Source(source.ID)
		if e != nil {
			return e
		}
		v.Permissions = []string{study.PermissionRead, study.PermissionSync}
		return tx.SaveSource(v)
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{oldKey, readKey, ""} {
		if out := request(t, h, "GET", "/api/v1/admin/identity-migration", key, nil); out.Code != 401 {
			t.Fatal(out.Code)
		}
		if out := request(t, h, "POST", "/api/v1/admin/identity-migration", key, map[string]any{"choices": []any{}}); out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	var preview study.IdentityMigrationPreview
	data(t, request(t, h, "GET", "/api/v1/admin/identity-migration", adminKey, nil), &preview)
	if preview.Pending != 1 || preview.Mixed != 0 {
		t.Fatal(preview)
	}
	for _, body := range []map[string]any{{}, {"choices": nil}} {
		if out := request(t, h, "POST", "/api/v1/admin/identity-migration", adminKey, body); out.Code != 400 {
			t.Fatal("implicit migration accepted", out.Code)
		}
	}
	clients, _ := s.Clients(ctx)
	if len(clients) != 0 {
		t.Fatal("GET/malformed POST migrated identities")
	}
	if out := request(t, h, "POST", "/api/v1/admin/sources/"+source.ID+"/key", adminKey, map[string]int64{"revision": source.Revision}); out.Code != 409 {
		t.Fatal("unmigrated query source rotated", out.Code)
	}
	var result study.IdentityMigrationResult
	data(t, request(t, h, "POST", "/api/v1/admin/identity-migration", adminKey, map[string]any{"choices": []any{}}), &result)
	if result.Converted != 1 || len(result.Clients) != 1 || len(result.NewKeys) != 0 {
		t.Fatal(result)
	}
	if out := request(t, h, "GET", "/api/v1/items", oldKey, nil); out.Code != 200 {
		t.Fatal("legacy query key was not transferred", out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/ingest/"+source.ID+"/status", oldKey, nil); out.Code != 401 {
		t.Fatal("same key retained source access", out.Code)
	}
	out := request(t, h, "GET", "/api/v1/admin/clients", adminKey, nil)
	if strings.Contains(out.Body.String(), oldKey) || strings.Contains(out.Body.String(), study.HashKey(oldKey)) {
		t.Fatal("credential leaked")
	}
}

func TestIntakeHistoryIsAdminOnlyAndRedactsMaterialContent(t *testing.T) {
	s, h, _ := managementApp(t, 1<<20)
	_, queryKey := managedClient(t, h, "query-reader", []string{"read", "sync"})
	source, sourceKey := create(t, h, false)
	var item study.Item
	data(t, request(t, h, "POST", "/api/v1/admin/items", adminKey, study.ManagedInput{SourceID: source.ID, Status: "draft", Content: study.Content{Title: "PRIVATE intake title", Content: "PRIVATE intake body"}}), &item)
	for _, key := range []string{queryKey, sourceKey, readKey, ""} {
		if out := request(t, h, "GET", "/api/v1/admin/intakes", key, nil); out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	var page study.IntakePage
	response := request(t, h, "GET", "/api/v1/admin/intakes?limit=1&offset=0&source_id="+source.ID, adminKey, nil)
	data(t, response, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Items[0].ID != item.ID || page.Items[0].Integration != "manual" {
		t.Fatal(page)
	}
	for _, private := range []string{"PRIVATE intake title", "PRIVATE intake body", sourceKey, queryKey, study.HashKey(sourceKey)} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("history leaked private content/credential")
		}
	}
	if _, e := s.IntakeHistory(context.Background(), "", 1, 0); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/v1/admin/intakes?limit=101", "/api/v1/admin/intakes?offset=-1", "/api/v1/admin/intakes?source_id=not-a-uuid"} {
		if out := request(t, h, "GET", path, adminKey, nil); out.Code != 400 {
			t.Fatal(path, out.Code)
		}
	}
}
