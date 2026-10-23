package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

var adminKey = strings.Repeat("a", 40)
var readKey = strings.Repeat("r", 40)

func app() (*study.Service, http.Handler) {
	s := study.NewService(study.NewMemoryStore())
	cfg := config.Config{Env: "test", AdminKey: adminKey, ReadKey: readKey, SSEMaxClients: 1, CORSOrigins: []string{"https://study.example"}}
	return s, New(cfg, s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func request(t *testing.T, h http.Handler, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var e error
		b, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func data(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if w.Code < 200 || w.Code >= 300 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &env); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(env.Data, out); e != nil {
		t.Fatal(e)
	}
}
func create(t *testing.T, h http.Handler, auto bool) (study.Source, string) {
	t.Helper()
	w := request(t, h, "POST", "/api/v1/admin/sources", adminKey, map[string]any{"slug": "lessons", "name": "学习资料", "auto_publish": auto})
	var out struct {
		Source study.Source `json:"source"`
		Key    string       `json:"access_key"`
	}
	data(t, w, &out)
	return out.Source, out.Key
}
func ingest(t *testing.T, h http.Handler, id, key, title string) study.Item {
	t.Helper()
	w := request(t, h, "POST", "/api/v1/ingest/"+id, key, map[string]any{"items": []study.Content{{ExternalID: "lesson-1", Title: title, Category: "编程", Tags: []string{"Go"}}}})
	var out study.IngestResult
	data(t, w, &out)
	return out.Items[0]
}

func TestAuthAndStrictDecoding(t *testing.T) {
	_, h := app()
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{{"GET", "/api/v1/admin/sources", "", 401}, {"GET", "/api/v1/admin/sources", readKey, 401}, {"GET", "/api/v1/items", "", 401}, {"GET", "/api/v1/items?token=" + readKey, "", 401}, {"GET", "/api/v1/health/live", "", 200}, {"GET", "/api/v1/health/ready", "", 200}, {"GET", "/missing", "", 404}, {"PATCH", "/api/v1/items", readKey, 405}} {
		w := request(t, h, tc.method, tc.path, tc.key, nil)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	for _, raw := range []string{`{"slug":"a","name":"A","unknown":1}`, `{"slug":"a","name":"A"} {}`, `{broken`} {
		r := httptest.NewRequest("POST", "/api/v1/admin/sources", strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(t, h, "POST", "/api/v1/admin/sources", adminKey, nil)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "/api/v1/admin/sources", strings.NewReader(`{"name":"`+strings.Repeat("x", 4<<20)+`"}`))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestApprovalFlowAndPermissionIsolation(t *testing.T) {
	_, h := app()
	source, key := create(t, h, false)
	item := ingest(t, h, source.ID, key, "草稿")
	if w := request(t, h, "GET", "/api/v1/admin/sources", key, nil); w.Code != 401 {
		t.Fatal("source key became admin")
	}
	if w := request(t, h, "GET", "/api/v1/items/"+item.ID, readKey, nil); w.Code != 404 {
		t.Fatal("draft became visible")
	}
	if w := request(t, h, "POST", "/api/v1/ingest/"+source.ID, adminKey, map[string]any{"items": []study.Content{{ExternalID: "admin", Title: "no"}}}); w.Code != 401 {
		t.Fatal("admin key accepted as source key")
	}
	var published study.Item
	data(t, request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/publish", adminKey, map[string]int64{"revision": item.Revision}), &published)
	var live study.PublicItem
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID, readKey, nil), &live)
	if live.Title != "草稿" {
		t.Fatal(live)
	}
	updated := ingest(t, h, source.ID, key, "新草稿")
	data(t, request(t, h, "GET", "/api/v1/items/"+item.ID, readKey, nil), &live)
	if live.Title != "草稿" {
		t.Fatal("live snapshot changed before approval")
	}
	if w := request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/publish", adminKey, map[string]int64{"revision": item.Revision}); w.Code != 409 {
		t.Fatal(w.Code)
	}
	data(t, request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/publish", adminKey, map[string]int64{"revision": updated.Revision}), &published)
	var list struct {
		Items []study.PublicItem `json:"items"`
	}
	data(t, request(t, h, "GET", "/api/v1/items?tag=Go&q="+"%E6%96%B0"+"&category="+"%E7%BC%96%E7%A8%8B", readKey, nil), &list)
	if len(list.Items) != 1 || list.Items[0].Title != "新草稿" {
		t.Fatal(list)
	}
	data(t, request(t, h, "POST", "/api/v1/admin/items/"+item.ID+"/unpublish", adminKey, map[string]int64{"revision": published.Revision}), &published)
	var changes study.ChangePage
	data(t, request(t, h, "GET", "/api/v1/changes?after=0", readKey, nil), &changes)
	if len(changes.Changes) != 3 || changes.Changes[2].Operation != "delete" {
		t.Fatal(changes)
	}
	w := request(t, h, "GET", "/api/v1/admin/sources/"+source.ID, adminKey, nil)
	if strings.Contains(w.Body.String(), study.HashKey(key)) || strings.Contains(w.Body.String(), key) {
		t.Fatal("key leaked")
	}
	for _, path := range []string{"/api/v1/items?limit=-1", "/api/v1/items?offset=10001", "/api/v1/changes?after=999", "/api/v1/changes?after=-1", "/api/v1/changes?source_id=invalid", "/api/v1/changes?limit=101"} {
		if w := request(t, h, "GET", path, readKey, nil); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
}
func TestRotateKeyAndSourceRevision(t *testing.T) {
	_, h := app()
	source, key := create(t, h, true)
	w := request(t, h, "POST", "/api/v1/admin/sources/"+source.ID+"/key", adminKey, map[string]int64{"revision": source.Revision})
	var rotated struct {
		Source study.Source `json:"source"`
		Key    string       `json:"access_key"`
	}
	data(t, w, &rotated)
	if w = request(t, h, "POST", "/api/v1/ingest/"+source.ID, key, map[string]any{"items": []study.Content{{ExternalID: "x", Title: "x"}}}); w.Code != 401 {
		t.Fatal(w.Code)
	}
	ingest(t, h, source.ID, rotated.Key, "published")
	w = request(t, h, "PUT", "/api/v1/admin/sources/"+source.ID, adminKey, map[string]any{"revision": rotated.Source.Revision, "slug": source.Slug, "name": "暂停", "kind": "manual", "enabled": false})
	var updated study.Source
	data(t, w, &updated)
	w = request(t, h, "POST", "/api/v1/ingest/"+source.ID, rotated.Key, map[string]any{"items": []study.Content{{ExternalID: "x", Title: "x"}}})
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestCORS(t *testing.T) {
	_, h := app()
	for _, origin := range []string{"https://study.example", "https://evil.example"} {
		r := httptest.NewRequest("OPTIONS", "/api/v1/items", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if origin == "https://study.example" {
			if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal(w.Code, w.Header())
			}
		} else if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}

type event struct {
	name string
	id   int64
	data string
}

func nextEvent(t *testing.T, reader *bufio.Reader) event {
	t.Helper()
	var out event
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			if out.name != "" {
				return out
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			out.name = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "id: ") {
			out.id, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
		}
		if strings.HasPrefix(line, "data: ") {
			out.data = strings.TrimPrefix(line, "data: ")
		}
	}
}
func openStream(t *testing.T, server *httptest.Server, path, lastID string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+readKey)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, e := server.Client().Do(req)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	return resp, cancel
}
func TestSSEReplayResumeAndConnectionLimit(t *testing.T) {
	service, h := app()
	source, key := create(t, h, true)
	item := ingest(t, h, source.ID, key, "first")
	server := httptest.NewServer(h)
	defer server.Close()
	resp, cancel := openStream(t, server, "/api/v1/stream?after=0", "")
	reader := bufio.NewReader(resp.Body)
	e := nextEvent(t, reader)
	if e.name != "change" || e.id != 1 || !strings.Contains(e.data, "first") {
		t.Fatal(e)
	}
	if e = nextEvent(t, reader); e.name != "checkpoint" || e.id != 1 {
		t.Fatal(e)
	}
	second, stopSecond := openStream(t, server, "/api/v1/stream", "")
	if second.StatusCode != 503 {
		t.Fatal(second.StatusCode)
	}
	second.Body.Close()
	stopSecond()
	cancel()
	resp.Body.Close()
	// Wait for the server-side cancellation to release its one stream slot.
	time.Sleep(30 * time.Millisecond)
	_, err := service.Ingest(context.Background(), source.ID, key, []study.Content{{ExternalID: item.ExternalID, Title: "second"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, cancel = openStream(t, server, "/api/v1/stream?after=0", "1")
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	e = nextEvent(t, bufio.NewReader(resp.Body))
	if e.name != "change" || e.id != 2 || strings.Contains(e.data, "first") {
		t.Fatal(e)
	}
}
func TestSSEFilteredCheckpoint(t *testing.T) {
	service, h := app()
	source, key := create(t, h, true)
	ingest(t, h, source.ID, key, "one")
	other, _, e := service.CreateSource(context.Background(), study.SourceInput{Slug: "other", Name: "other"})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	resp, cancel := openStream(t, server, "/api/v1/stream?source_id="+other.ID, "")
	defer cancel()
	defer resp.Body.Close()
	evt := nextEvent(t, bufio.NewReader(resp.Body))
	if evt.name != "checkpoint" || evt.id != 1 {
		t.Fatal(evt)
	}
}
