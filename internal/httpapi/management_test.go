package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

func managementApp(t *testing.T, limit int64) (*study.Service, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	s := study.NewService(study.NewMemoryStore())
	if _, _, err := s.EnsureAdministrator(context.Background(), study.AdministratorSettings{Email: "admin@example.com", Password: "administrator-test-password"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: readKey, DataDir: dir, MaxUploadBytes: limit}
	return s, New(cfg, s, nil, slog.New(slog.NewTextHandler(io.Discard, nil))), dir
}
func browserRequest(t *testing.T, h http.Handler, method, path, origin string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func wavBytes(n int) []byte {
	b := make([]byte, 44+n)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 8000)
	binary.LittleEndian.PutUint32(b[28:], 16000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	return b
}
func multipartRequest(t *testing.T, h http.Handler, path, key, name string, content []byte, metadata any) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	raw, _ := json.Marshal(metadata)
	if e := m.WriteField("metadata", string(raw)); e != nil {
		t.Fatal(e)
	}
	part, e := m.CreateFormFile("files", name)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = part.Write(content); e != nil {
		t.Fatal(e)
	}
	m.Close()
	r := httptest.NewRequest("POST", path, &b)
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestManagementSessionCSRFAndStaticBundle(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	w := request(t, h, "GET", "/admin/", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/admin/app.js") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), adminKey) {
		t.Fatal("secret embedded in HTML")
	}
	for _, path := range []string{"/admin/app.js", "/admin/app.css"} {
		if out := request(t, h, "GET", path, "", nil); out.Code != 200 || out.Body.Len() < 1000 {
			t.Fatal(path, out.Code)
		}
	}
	if out := request(t, h, "GET", "/admin/not-found.js", "", nil); out.Code != 404 {
		t.Fatal(out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/admin/workspace", readKey, nil); out.Code != 401 {
		t.Fatal("read key became admin", out.Code)
	}
	if out := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "wrong-password"}); out.Code != 401 {
		t.Fatal(out.Code)
	}
	w = request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 8*3600 {
		t.Fatal(cookie)
	}
	if out := browserRequest(t, h, "GET", "/api/v1/auth/session", "", cookie, nil); out.Code != 200 {
		t.Fatal(out.Code)
	}
	settings := map[string]any{"name": "会话管理员", "compact": true, "revision": 0}
	if out := browserRequest(t, h, "PUT", "/api/v1/admin/settings", "", cookie, settings); out.Code != 403 {
		t.Fatal("missing Origin accepted", out.Code)
	}
	if out := browserRequest(t, h, "PUT", "/api/v1/admin/settings", "https://evil.example", cookie, settings); out.Code != 403 {
		t.Fatal("cross origin accepted", out.Code)
	}
	if out := browserRequest(t, h, "PUT", "/api/v1/admin/settings", "http://example.com", cookie, settings); out.Code != 200 {
		t.Fatal("same-origin denied", out.Code, out.Body.String())
	}
	if out := browserRequest(t, h, "POST", "/api/v1/auth/logout", "http://example.com", cookie, map[string]any{}); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if out := browserRequest(t, h, "GET", "/api/v1/auth/session", "", cookie, nil); out.Code != 401 {
		t.Fatal("logged-out cookie remained valid", out.Code)
	}
}

func TestManagedMediaUploadRangeReviewAndDeletion(t *testing.T) {
	s, h, dir := managementApp(t, 1<<20)
	body := wavBytes(1600)
	input := study.ManagedInput{Status: "pending", Content: study.Content{Kind: "audio", Title: "真实音频入库", Category: "语言学习"}}
	w := multipartRequest(t, h, "/api/v1/admin/uploads", adminKey, "lesson.wav", body, input)
	var item study.Item
	data(t, w, &item)
	if len(item.Draft.Assets) != 1 || item.Status != "pending" {
		t.Fatal(item)
	}
	asset := item.Draft.Assets[0]
	if asset.URL != "/api/v1/media/"+asset.ID || asset.MIME != "audio/wav" {
		t.Fatal(asset)
	}
	disk, e := os.ReadFile(filepath.Join(dir, "uploads", asset.ID))
	if e != nil || !bytes.Equal(disk, body) {
		t.Fatal("file was not durably saved", e)
	}
	if out := request(t, h, "GET", asset.URL, readKey, nil); out.Code != 404 {
		t.Fatal("private file readable", out.Code)
	}
	if out := request(t, h, "GET", asset.URL, "", nil); out.Code != 401 {
		t.Fatal(out.Code)
	}
	r := httptest.NewRequest("GET", asset.URL, nil)
	r.Header.Set("Authorization", "Bearer "+adminKey)
	r.Header.Set("Range", "bytes=0-15")
	rangeResponse := httptest.NewRecorder()
	h.ServeHTTP(rangeResponse, r)
	if rangeResponse.Code != 206 || !bytes.Equal(rangeResponse.Body.Bytes(), body[:16]) {
		t.Fatal("range response", rangeResponse.Code, rangeResponse.Body.Len())
	}
	data(t, request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/publish", adminKey, map[string]int64{"revision": item.Revision}), &item)
	if out := request(t, h, "GET", asset.URL, readKey, nil); out.Code != 200 || !bytes.Equal(out.Body.Bytes(), body) {
		t.Fatal("published file unavailable", out.Code)
	}
	if out := request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/delete", adminKey, map[string]int64{"revision": item.Revision - 1}); out.Code != 409 {
		t.Fatal(out.Code)
	}
	if out := request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/delete", adminKey, map[string]int64{"revision": item.Revision}); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if out := request(t, h, "GET", asset.URL, readKey, nil); out.Code != 404 {
		t.Fatal("deleted file stayed public", out.Code)
	}
	changes, e := s.Changes(context.Background(), 0, 100, nil)
	if e != nil || changes.Changes[len(changes.Changes)-1].Operation != "delete" {
		t.Fatal(changes, e)
	}
}

func TestUploadRejectsMismatchHTMLSVGAndSizeAndCleansDisk(t *testing.T) {
	s, h, dir := managementApp(t, 1024)
	input := study.ManagedInput{Status: "pending", Content: study.Content{Kind: "audio", Title: "测试上传"}}
	for _, tc := range []struct {
		name   string
		bytes  []byte
		status int
	}{{"active.svg", []byte(`<svg onload="alert(1)"></svg>`), 400}, {"active.html", []byte(`<script>alert(1)</script>`), 400}, {"fake.mp3", []byte(`<html>not audio</html>`), 400}, {"large.wav", wavBytes(1500), 413}, {"empty.wav", nil, 400}} {
		w := multipartRequest(t, h, "/api/v1/admin/uploads", adminKey, tc.name, tc.bytes, input)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
		entries, _ := os.ReadDir(filepath.Join(dir, "uploads"))
		if len(entries) != 0 {
			t.Fatal("rejected upload leaked files", tc.name)
		}
		workspace, e := s.Workspace(context.Background())
		if e != nil || workspace.Summary.Total != 0 || workspace.Summary.AssetCount != 0 {
			t.Fatal(workspace, e)
		}
	}
	input.Content.Kind = "video"
	w := multipartRequest(t, h, "/api/v1/admin/uploads", adminKey, "wrong-kind.wav", wavBytes(100), input)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "uploads"))
	if len(entries) != 0 {
		t.Fatal("mismatch leaked a file")
	}
}

func TestUTF8TextUploadAndSourceUploadOwnership(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	text := strings.Repeat("知识连接，学习成长。", 80)
	input := study.ManagedInput{Status: "draft", Content: study.Content{Kind: "article", Title: "中文 Markdown"}}
	w := multipartRequest(t, h, "/api/v1/admin/uploads", adminKey, "notes.md", []byte(text), input)
	var article study.Item
	data(t, w, &article)
	if article.Draft.Content != text || article.Draft.Format != "markdown" {
		t.Fatal("UTF8 text did not survive import")
	}
	source, key := create(t, h, false)
	w = multipartRequest(t, h, "/api/v1/ingest/"+source.ID+"/files", key, "client.wav", wavBytes(100), study.Content{ExternalID: "client-audio", Kind: "audio", Title: "客户端上传"})
	var ingested study.IngestResult
	data(t, w, &ingested)
	if len(ingested.Items) != 1 || ingested.Items[0].SourceID != source.ID || ingested.Items[0].Draft.Assets[0].SourceID != source.ID {
		t.Fatal(ingested)
	}
	if w = multipartRequest(t, h, "/api/v1/ingest/"+source.ID+"/files", adminKey, "client.wav", wavBytes(100), study.Content{Title: "不能代替来源凭据"}); w.Code != 401 {
		t.Fatal("admin became source", w.Code)
	}
}

func TestManagementEndpointsPersistAndExposeClientQueries(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	var item study.Item
	data(t, request(t, h, "POST", "/api/v1/admin/items", adminKey, study.ManagedInput{Status: "draft", Content: study.Content{Kind: "article", Title: "API 学习资料", Content: "安全正文", Category: "学习方法"}}), &item)
	if w := request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/publish", adminKey, map[string]int64{"revision": item.Revision}); w.Code != 400 {
		t.Fatal("draft skipped submit", w.Code)
	}
	data(t, request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/submit", adminKey, map[string]int64{"revision": item.Revision}), &item)
	data(t, request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/publish", adminKey, map[string]int64{"revision": item.Revision}), &item)
	var page struct {
		Items []study.Item `json:"items"`
		Total int          `json:"total"`
	}
	data(t, request(t, h, "GET", "/api/v1/admin/items?kind=article&status=published&limit=1", adminKey, nil), &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Draft.Content != "" {
		t.Fatal(page)
	}
	_, queryKey := managedClient(t, h, "learning", []string{"read", "sync"})
	var settings study.WorkspaceSettings
	data(t, request(t, h, "PUT", "/api/v1/admin/settings", adminKey, study.WorkspaceSettings{Name: "空间管理员"}), &settings)
	var workspace study.Workspace
	data(t, request(t, h, "GET", "/api/v1/admin/workspace", adminKey, nil), &workspace)
	if workspace.Settings.Name != "空间管理员" || len(workspace.Clients) != 1 {
		t.Fatal(workspace)
	}
	var public study.PublicItem
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID, queryKey, nil), &public)
	if public.Title != "API 学习资料" {
		t.Fatal(public)
	}
	if out := request(t, h, "POST", "/api/v1/admin/collections", adminKey, map[string]any{}); out.Code != 404 {
		t.Fatal(out.Code)
	}
}
