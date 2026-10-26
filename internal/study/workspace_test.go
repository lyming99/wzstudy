package study

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func managedArticle(t *testing.T, s *Service, status, title string) Item {
	t.Helper()
	v, e := s.SaveManaged(context.Background(), "", ManagedInput{Status: status, Content: Content{Title: title, Kind: "article", Content: "学习正文", Category: "学习方法", Author: "管理员"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func approveManaged(t *testing.T, s *Service, v Item) Item {
	t.Helper()
	var e error
	if ReviewState(v) == "draft" {
		v, e = s.Submit(context.Background(), v.ID, v.Revision)
		if e != nil {
			t.Fatal(e)
		}
	}
	v, e = s.Publication(context.Background(), v.ID, v.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func clientSource(t *testing.T, s *Service) (Source, string) {
	t.Helper()
	v, key, e := s.CreateSource(context.Background(), SourceInput{Slug: "client-" + uuid.NewString(), Name: "学习接入端", Kind: "manual"})
	if e != nil {
		t.Fatal(e)
	}
	return v, key
}

func TestManagedReviewAndPublicSnapshot(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	v := managedArticle(t, s, "draft", "原始标题")
	if _, e := s.Publication(ctx, v.ID, v.Revision, true); e != ErrInvalid {
		t.Fatalf("draft bypassed review: %v", e)
	}
	before, _ := s.Changes(ctx, 0, 100, nil)
	if len(before.Changes) != 0 {
		t.Fatal(before)
	}
	v = approveManaged(t, s, v)
	oldRev := v.Revision
	liveTitle := v.Published.Title
	v, e := s.SaveManaged(ctx, v.ID, ManagedInput{Revision: v.Revision, Status: "pending", Content: Content{Title: "尚未批准的标题", Content: "私有新正文"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if v.Published.Title != liveTitle || ReviewState(v) != "pending" {
		t.Fatal("draft replaced live snapshot")
	}
	if _, e = s.Publication(ctx, v.ID, oldRev, true); e != ErrConflict {
		t.Fatal("stale approval", e)
	}
	if _, e = s.SaveManaged(ctx, v.ID, ManagedInput{Revision: v.Revision - 1, Status: "draft", Content: Content{Title: "过期写入"}}, nil); e != ErrConflict {
		t.Fatal(e)
	}
	v = approveManaged(t, s, v)
	if v.Published.Title != "尚未批准的标题" {
		t.Fatal(v)
	}
	n, _ := s.Changes(ctx, 0, 100, nil)
	v, e = s.Publication(ctx, v.ID, v.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := s.Changes(ctx, 0, 100, nil)
	if len(n.Changes) != len(after.Changes) {
		t.Fatal("repeated approval created an event")
	}
	if len(v.History) < 4 {
		t.Fatal("missing audit events")
	}
}

func TestManagedAssetsAreCanonicalAndOwned(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	asset := Asset{ID: uuid.NewString(), Name: "lesson.wav", MIME: "audio/wav", Size: 100, CreatedAt: s.Now()}
	v, e := s.SaveManaged(ctx, "", ManagedInput{Status: "pending", Content: Content{Title: "音频", Kind: "audio", Assets: []Asset{{ID: asset.ID, Name: "伪造名称", URL: "https://wrong.invalid"}}}}, []Asset{asset})
	if e != nil {
		t.Fatal(e)
	}
	saved := v.Draft.Assets[0]
	if saved.Name != asset.Name || saved.URL != "/api/v1/media/"+asset.ID || saved.SourceID != v.SourceID {
		t.Fatal(saved)
	}
	if _, e = s.Asset(ctx, asset.ID, false); e != ErrNotFound {
		t.Fatal("private asset became readable", e)
	}
	if _, e = s.Asset(ctx, asset.ID, true); e != nil {
		t.Fatal(e)
	}
	v = approveManaged(t, s, v)
	if _, e = s.Asset(ctx, asset.ID, false); e != nil {
		t.Fatal(e)
	}
	source, key := clientSource(t, s)
	if _, e = s.Ingest(ctx, source.ID, key, []Content{{ExternalID: "stolen", Title: "偷用附件", Kind: "audio", Assets: []Asset{{ID: asset.ID}}}}); e != ErrUnauthorized {
		t.Fatal("source referenced another source's asset", e)
	}
	if e = s.DeleteItem(ctx, v.ID, v.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Asset(ctx, asset.ID, true); e != ErrNotFound {
		t.Fatal("deleted item asset stayed visible", e)
	}
	if _, e = s.Item(ctx, v.ID); e != ErrNotFound {
		t.Fatal(e)
	}
	changes, _ := s.Changes(ctx, 0, 100, nil)
	if changes.Changes[len(changes.Changes)-1].Operation != "delete" {
		t.Fatal(changes)
	}
}

func TestInvalidUploadMetadataRollsBackAssets(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	asset := Asset{ID: uuid.NewString(), Name: "image.png", MIME: "image/png", Size: 50}
	_, e := s.SaveManaged(ctx, "", ManagedInput{Status: "pending", Content: Content{Title: "错配的视频", Kind: "video", Assets: []Asset{asset}}}, []Asset{asset})
	if e != ErrInvalid {
		t.Fatal(e)
	}
	_ = s.Store.View(ctx, func(tx Tx) error {
		a, _ := tx.Records("asset")
		sources, _ := tx.Sources()
		if len(a) != 0 || len(sources) != 0 {
			t.Fatal("failed transaction leaked asset/source")
		}
		return nil
	})
	source, key := clientSource(t, s)
	out, e := s.IngestAssets(ctx, source.ID, key, Content{ExternalID: "card-1", Title: "卡片轮播", Kind: "carousel", Assets: []Asset{asset}}, []Asset{asset})
	if e != nil || len(out.Items) != 1 {
		t.Fatal(e, out)
	}
	if out.Items[0].Draft.Assets[0].SourceID != source.ID {
		t.Fatal(out)
	}
}

func TestWorkspaceFiltersTotalsClientsAndSettings(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	a := managedArticle(t, s, "pending", "知识积累")
	_ = a
	_ = managedArticle(t, s, "draft", "草稿资料")
	published := approveManaged(t, s, managedArticle(t, s, "pending", "每日学习"))
	page, total, e := s.AdminPage(ctx, Filter{Status: "published", Kind: "article", Limit: 1, Admin: true})
	if e != nil || total != 1 || len(page) != 1 || page[0].ID != published.ID || page[0].Draft.Content != "" || page[0].Published.Content.Content != "" {
		t.Fatal(page, total, e)
	}
	_, total, e = s.AdminPage(ctx, Filter{Query: "管理员", Limit: 20, Admin: true})
	if e != nil || total != 3 {
		t.Fatal(total, e)
	}
	w, e := s.Workspace(ctx)
	if e != nil || w.Summary.Total != 3 || w.Summary.Pending != 1 || w.Summary.Published != 1 || w.Summary.Draft != 1 || len(w.Summary.Trends) != 7 {
		t.Fatal(w, e)
	}
	settings, e := s.SaveSettings(ctx, WorkspaceSettings{Name: "小知", Compact: true})
	if e != nil || settings.Revision != 1 {
		t.Fatal(settings, e)
	}
	if _, e = s.SaveSettings(ctx, WorkspaceSettings{Name: "冲突"}); e != ErrConflict {
		t.Fatal(e)
	}
	c, _, e := s.CreateClient(ctx, ClientInput{Slug: "query", Name: "Query"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateClient(ctx, c.ID, c.Revision-1, ClientInput{Slug: c.Slug, Name: c.Name}); e != ErrConflict {
		t.Fatal(e)
	}
	w, e = s.Workspace(ctx)
	if e != nil || len(w.Clients) != 1 || w.Settings.Name != "小知" {
		t.Fatal(e, w)
	}
}

func TestFileStoreDurableSecretsAndTransactionRollback(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "workspace.json")
	store, e := OpenFileStore(file)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(store)
	source, key := clientSource(t, s)
	_ = approveManaged(t, s, managedArticle(t, s, "pending", "重启后仍保留"))
	_, e = s.SaveSettings(ctx, WorkspaceSettings{Name: "文件存储管理员"})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.CreateClient(ctx, ClientInput{Slug: "durable", Name: "持久查询客户端"}); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(before), key) {
		t.Fatal("plaintext source key was persisted")
	}
	e = store.Update(ctx, func(tx Tx) error {
		if e := tx.SaveRecord("collection", "rollback", []byte(`{"name":"不应保存"}`)); e != nil {
			return e
		}
		return errors.New("rollback")
	})
	if e == nil {
		t.Fatal("missing rollback")
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("failed transaction modified disk")
	}
	reopened, e := OpenFileStore(file)
	if e != nil {
		t.Fatal(e)
	}
	s2 := NewService(reopened)
	w, e := s2.Workspace(ctx)
	if e != nil || w.Summary.Total != 1 || len(w.Clients) != 1 || w.Settings.Name != "文件存储管理员" {
		t.Fatal(w, e)
	}
	if _, e = s2.Ingest(ctx, source.ID, key, []Content{{ExternalID: "after-restart", Title: "来源凭据仍可验证"}}); e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e = reopened.Update(cancelled, func(tx Tx) error { return tx.SaveRecord("settings", "cancelled", []byte(`{}`)) }); e != context.Canceled {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte(`corrupted`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenFileStore(file); e == nil {
		t.Fatal("corrupt file was silently reset")
	}
}

func TestClientPullDoesNotRewriteFile(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "workspace.json")
	store, e := OpenFileStore(file)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(store)
	_ = managedArticle(t, s, "draft", "不会无谓写盘")
	before, e := os.Stat(file)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(15 * time.Millisecond)
	if _, e = s.Items(ctx, Filter{Limit: 10}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Changes(ctx, 0, 100, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Workspace(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ := os.Stat(file)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("client pull rewrote file")
	}
}
