package collect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wzstudy-agent/internal/tool"
)

const samplePage = `<!doctype html><html><head><title>示例文章</title>
<meta name="description" content="这是一篇示例文章"></head>
<body><nav>导航</nav><article><h1>示例文章</h1><p>第一段内容。</p><p>第二段内容。</p>
<script>alert(1)</script></article></body></html>`

func TestFetcherExtractsReadablePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(samplePage))
	}))
	defer server.Close()
	fetcher := NewFetcher(1<<20, true)
	result, err := fetcher.Handle(context.Background(), tool.Call{Name: "fetch_url", Arguments: json.RawMessage(`{"url":"` + server.URL + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Text        string `json:"text"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("content is not JSON: %v", err)
	}
	if payload.Title != "示例文章" || !strings.Contains(payload.Description, "示例文章") {
		t.Fatalf("unexpected extraction: %+v", payload)
	}
	if !strings.Contains(payload.Text, "第一段内容") || strings.Contains(payload.Text, "alert(1)") {
		t.Fatalf("body text is wrong: %q", payload.Text)
	}
}

func TestFetcherBlocksLoopbackByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should not be reachable"))
	}))
	defer server.Close()
	fetcher := NewFetcher(1<<20, false)
	if _, err := fetcher.Handle(context.Background(), tool.Call{Name: "fetch_url", Arguments: json.RawMessage(`{"url":"` + server.URL + `"}`)}); err == nil {
		t.Fatal("loopback target must be rejected when private networking is disabled")
	}
}

func TestFetcherTruncatesLargeResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 5000)))
	}))
	defer server.Close()
	fetcher := NewFetcher(1<<20, true)
	result, err := fetcher.Handle(context.Background(), tool.Call{Name: "fetch_url", Arguments: json.RawMessage(`{"url":"` + server.URL + `","max_bytes":1024}`)})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Truncated bool `json:"truncated"`
	}
	_ = json.Unmarshal([]byte(result.Content), &payload)
	if !payload.Truncated {
		t.Fatalf("expected truncation: %s", result.Content)
	}
}
