package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wzstudy-agent/internal/tool"
)

const testSubtitleSRT = "1\n00:00:00,000 --> 00:00:02,000\n真实字幕\n"
const testSubtitleVTT = "WEBVTT\n\n00:00.000 --> 00:02.000\nReal subtitles\n"

func subtitleToolCall(t *testing.T, input any) tool.Call {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Call{Arguments: raw}
}

func TestFetchSubtitleKeepsFullTextAndTaskScopedReference(t *testing.T) {
	// Generic fetch_url truncates model-visible text at 24,000 bytes. Subtitle
	// fetching must instead retain the entire track without echoing it to the LLM.
	text := testSubtitleSRT + strings.Repeat("真实字幕内容\n", 4000)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-subrip")
		_, _ = w.Write([]byte("\ufeff" + text))
	}))
	defer remote.Close()
	collector := NewSubtitleCollector(true, nil)
	result, err := collector.Handle(context.Background(), subtitleToolCall(t, map[string]any{"url": remote.URL + "/zh.srt"}))
	if err != nil || result.IsError {
		t.Fatalf("fetch subtitle: %+v %v", result, err)
	}
	var output struct {
		Available bool   `json:"available"`
		Ref       string `json:"subtitle_ref"`
		Format    string `json:"format"`
		Size      int    `json:"size"`
		Uploaded  bool   `json:"uploaded"`
	}
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Available || output.Ref == "" || output.Format != "srt" || output.Size != len(text) || output.Uploaded || strings.Contains(result.Content, "真实字幕内容") {
		t.Fatalf("unexpected preparation result: %+v %s", output, result.Content)
	}
	track, ok := collector.resolve(output.Ref)
	if !ok || track.Content != text || track.Format != "srt" {
		t.Fatal("full subtitle text was lost or truncated")
	}
	if _, ok := NewSubtitleCollector(true, nil).resolve(output.Ref); ok {
		t.Fatal("subtitle reference leaked into another task")
	}
	if _, ok := (*SubtitleCollector)(nil).resolve(output.Ref); ok {
		t.Fatal("nil collector resolved a reference")
	}
}

func TestFetchSubtitleOptionalFailuresAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, content, format string
		status                int
		wantError             bool
	}{
		{name: "missing", status: 404},
		{name: "forbidden", status: 403},
		{name: "gone", status: 410},
		{name: "empty response", status: 204},
		{name: "valid vtt", status: 200, content: testSubtitleVTT, format: "vtt"},
		{name: "html is not subtitles", status: 200, content: "<html>login required</html>", wantError: true},
		{name: "oversize", status: 200, content: testSubtitleSRT + strings.Repeat("x", maximumSubtitleBytes), wantError: true},
		{name: "invalid utf8", status: 200, content: "\xff" + testSubtitleSRT, wantError: true},
		{name: "unsupported format", status: 200, content: testSubtitleSRT, format: "ass", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.content))
			}))
			defer remote.Close()
			collector := NewSubtitleCollector(true, nil)
			result, err := collector.Handle(context.Background(), subtitleToolCall(t, map[string]any{"url": remote.URL + "/captions", "format": tc.format}))
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v %+v", err, result)
			}
			if tc.status != 200 && !tc.wantError {
				if result.IsError || !strings.Contains(result.Content, `"available":false`) || len(collector.tracks) != 0 {
					t.Fatalf("unavailable subtitles must be an optional result: %+v", result)
				}
			}
			if tc.wantError && len(collector.tracks) != 0 {
				t.Fatal("invalid or truncated subtitles were retained")
			}
		})
	}
}

func TestSubtitleFetchNetworkAndTaskDomainBoundaries(t *testing.T) {
	var hits atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://localhost:"+strings.Split(r.Host, ":")[1]+"/caption.srt", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(testSubtitleSRT))
	}))
	defer remote.Close()
	for _, collector := range []*SubtitleCollector{
		NewSubtitleCollector(false, nil),
		NewSubtitleCollector(true, []string{"example.org"}),
	} {
		if _, err := collector.Handle(context.Background(), subtitleToolCall(t, map[string]any{"url": remote.URL + "/caption.srt"})); err == nil {
			t.Fatal("private or out-of-task target was accepted")
		}
	}
	if hits.Load() != 0 {
		t.Fatal("blocked target was contacted")
	}
	collector := NewSubtitleCollector(true, []string{"127.0.0.1"})
	if _, err := collector.Handle(context.Background(), subtitleToolCall(t, map[string]any{"url": remote.URL + "/redirect"})); err == nil || hits.Load() != 1 {
		t.Fatalf("redirect escaped task domains: %v, hits=%d", err, hits.Load())
	}
	for _, target := range []string{"file:///private.srt", "http://user:password@example.org/sub.srt"} {
		if _, err := collector.Handle(context.Background(), subtitleToolCall(t, map[string]any{"url": target})); err == nil {
			t.Fatalf("unsafe URL was accepted: %s", target)
		}
	}
}

func TestNormalizeSubtitleText(t *testing.T) {
	for _, tc := range []struct {
		format, text string
		valid        bool
	}{
		{"srt", testSubtitleSRT, true},
		{"srt", strings.ReplaceAll(testSubtitleSRT, "\n", "\r\n"), true},
		{"vtt", testSubtitleVTT, true},
		{"vtt", "\ufeff" + testSubtitleVTT, true},
		{"srt", "", false},
		{"srt", "这是摘要，不是字幕", false},
		{"srt", testSubtitleSRT + "\x00", false},
		{"srt", testSubtitleVTT, false},
		{"vtt", testSubtitleSRT, false},
		{"ass", testSubtitleSRT, false},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.format, len(tc.text)), func(t *testing.T) {
			_, err := normalizeSubtitleText(tc.format, tc.text)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
