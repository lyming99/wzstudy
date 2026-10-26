package study

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Uses the existing isolated-schema fixture. Set TEST_DATABASE_URL to a
// dedicated PostgreSQL test database to run migration and workspace coverage.
func TestPostgresManagementWorkspace(t *testing.T) {
	s := fixture(t, "postgres")
	ctx := context.Background()
	now := time.Now().UTC()
	s.Now = func() time.Time { return now }
	if e := s.Store.Ping(ctx); e != nil {
		t.Fatal("management migration readiness", e)
	}
	a := Asset{ID: uuid.NewString(), Name: "postgres-audio.wav", MIME: "audio/wav", Size: 100, CreatedAt: now}
	item, e := s.SaveManaged(ctx, "", ManagedInput{Status: "pending", Content: Content{Title: "PostgreSQL 已审核音频", Kind: "audio", Category: "语言学习", Author: "测试管理员", Assets: []Asset{a}}}, []Asset{a})
	if e != nil {
		t.Fatal(e)
	}
	item = approveManaged(t, s, item)
	page, total, e := s.AdminPage(ctx, Filter{Kind: "audio", Status: "published", Query: "测试管理员", Limit: 10})
	if e != nil || total != 1 || len(page) != 1 {
		t.Fatal(page, total, e)
	}
	client, key, e := s.CreateClient(ctx, ClientInput{Slug: "query", Name: "PG query"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveSettings(ctx, WorkspaceSettings{Name: "PG 管理员"}); e != nil {
		t.Fatal(e)
	}
	_, e = s.SaveManaged(ctx, item.ID, ManagedInput{Revision: item.Revision, Status: "pending", Content: Content{Title: "未审核的私有变更", Kind: "audio", Category: "语言学习", Assets: []Asset{a}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, Filter{Kind: "audio", Limit: 10})
	if e != nil || len(items) != 1 || items[0].Published.Title != "PostgreSQL 已审核音频" {
		t.Fatal(items, e)
	}
	changes, e := s.Changes(ctx, 0, 100, nil)
	if e != nil || len(changes.Changes) != 1 || changes.Changes[0].Item.Title != "PostgreSQL 已审核音频" {
		t.Fatal(changes, e)
	}
	w, e := s.Workspace(ctx)
	if e != nil || w.Summary.AssetCount != 1 || w.Summary.Total != 1 || w.Settings.Name != "PG 管理员" {
		t.Fatal(w, e)
	}
	if _, e = s.ClientForKey(ctx, key, PermissionRead); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Client(ctx, client.ID); e != nil {
		t.Fatal(e)
	}
}
