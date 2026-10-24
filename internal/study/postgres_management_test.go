package study

import (
	"context"
	"sync"
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
	target, key := clientSource(t, s)
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
	collection, e := s.SaveCollection(ctx, "", Collection{Name: "语言学习", Category: "语言学习", Color: "blue"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveSettings(ctx, WorkspaceSettings{Name: "PG 管理员"}); e != nil {
		t.Fatal(e)
	}
	plan, e := s.CreatePlan(ctx, PushPlan{Title: "PG 定时投递", ItemIDs: []string{item.ID}, TargetIDs: []string{target.ID}, ScheduledAt: now.Add(time.Minute)})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SaveManaged(ctx, item.ID, ManagedInput{Revision: item.Revision, Status: "pending", Content: Content{Title: "未审核的私有变更", Kind: "audio", Category: "语言学习", Assets: []Asset{a}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.DispatchDue(ctx) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	inbox, e := s.Inbox(ctx, target.ID, key, 0, 100)
	if e != nil || len(inbox.Deliveries) != 1 || inbox.Deliveries[0].Item.Title != "PostgreSQL 已审核音频" {
		t.Fatal(inbox, e)
	}
	w, e := s.Workspace(ctx)
	if e != nil || w.Summary.AssetCount != 1 || w.Summary.Total != 1 || len(w.Plans) != 1 || w.Plans[0].ID != plan.ID || w.Plans[0].Status != "done" || w.Settings.Name != "PG 管理员" {
		t.Fatal(w, e)
	}
	if e = s.DeleteCollection(ctx, collection.ID, collection.Revision); e != nil {
		t.Fatal(e)
	}
}
