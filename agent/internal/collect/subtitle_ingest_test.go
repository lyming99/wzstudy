package collect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wzstudy-agent/internal/serverapi"
)

func TestIngestUploadsOnlySelectedSubtitles(t *testing.T) {
	for _, selected := range []bool{false, true} {
		name := "fetched_but_not_selected"
		if selected {
			name = "ref_and_inline_tracks"
		}
		t.Run(name, func(t *testing.T) {
			var wireValue atomic.Value
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/en.vtt" {
					_, _ = w.Write([]byte(testSubtitleVTT))
					return
				}
				if r.URL.Path != "/api/v1/agent/tasks/task-1/items" || r.Header.Get("Authorization") != "Bearer wzag-test" {
					t.Error("wrong task upload endpoint or authentication")
				}
				raw, _ := io.ReadAll(r.Body)
				wireValue.Store(string(raw))
				_, _ = w.Write([]byte(`{"data":{"created":1,"items":[{"id":"item-1","revision":1,"status":"pending"}]}}`))
			}))
			defer remote.Close()
			collector := NewSubtitleCollector(true, nil)
			fetched, err := collector.Handle(context.Background(), subtitleToolCall(t, map[string]any{"url": remote.URL + "/en.vtt"}))
			if err != nil {
				t.Fatal(err)
			}
			var prepared struct {
				Ref string `json:"subtitle_ref"`
			}
			if err := json.Unmarshal([]byte(fetched.Content), &prepared); err != nil {
				t.Fatal(err)
			}
			client, _ := serverapi.New(remote.URL, "wzag-test", "test")
			stats := &serverapi.Stats{}
			task := &serverapi.Task{ID: "task-1", Attempts: 2, Constraints: serverapi.Constraints{MaxItems: 1, ContentType: "video"}}
			ingestor := NewIngestor(client, task, 2, stats, collector)
			item := map[string]any{"external_id": "video-1", "kind": "video", "title": "Video", "assets": []map[string]any{{"id": "asset-1"}}}
			if selected {
				item["subtitles"] = []map[string]any{
					{"id": "en", "language": "en", "ref": prepared.Ref},
					{"id": "zh", "language": "zh-CN", "content": testSubtitleSRT},
				}
			}
			result, err := ingestor.Handle(context.Background(), subtitleToolCall(t, map[string]any{"items": []any{item}}))
			if err != nil || result.IsError || stats.ItemsCreated != 1 {
				t.Fatalf("ingest: %+v %v %+v", result, err, stats)
			}
			wire, _ := wireValue.Load().(string)
			var uploaded struct {
				Attempt int                 `json:"attempt"`
				Items   []serverapi.Content `json:"items"`
			}
			if err := json.Unmarshal([]byte(wire), &uploaded); err != nil || uploaded.Attempt != 2 || len(uploaded.Items) != 1 {
				t.Fatalf("invalid upload: %s %v", wire, err)
			}
			tracks := uploaded.Items[0].Subtitles
			if selected {
				if len(tracks) != 2 || tracks[0].Content != testSubtitleVTT || tracks[0].Format != "vtt" || tracks[0].Language != "en" || tracks[1].Content != testSubtitleSRT || tracks[1].Format != "srt" {
					t.Fatalf("subtitle content or metadata lost in HTTP upload: %+v", tracks)
				}
				if strings.Contains(wire, prepared.Ref) || strings.Contains(wire, `"ref"`) {
					t.Fatal("task-local references were sent as server content")
				}
			} else if len(tracks) != 0 || strings.Contains(wire, `"subtitles"`) {
				t.Fatal("fetching alone must not attach or upload subtitles")
			}
		})
	}
}

func TestIngestWithoutSubtitlesRemainsSupported(t *testing.T) {
	for _, kind := range []string{"video", "audio"} {
		t.Run(kind, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Items []serverapi.Content `json:"items"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Items) != 1 || in.Items[0].Kind != kind || len(in.Items[0].Subtitles) != 0 {
					t.Errorf("unexpected optional subtitle payload: %+v %v", in, err)
				}
				_, _ = w.Write([]byte(`{"data":{"created":1,"items":[]}}`))
			}))
			defer remote.Close()
			client, _ := serverapi.New(remote.URL, "wzag-test", "test")
			stats := &serverapi.Stats{}
			ingestor := NewIngestor(client, &serverapi.Task{ID: "task", Constraints: serverapi.Constraints{MaxItems: 1}}, 1, stats, nil)
			_, err := ingestor.Handle(context.Background(), subtitleToolCall(t, map[string]any{"items": []any{map[string]any{"kind": kind, "external_id": kind, "title": kind, "assets": []any{map[string]any{"id": "asset"}}}}}))
			if err != nil || stats.ItemsCreated != 1 {
				t.Fatal(err, stats)
			}
		})
	}
}

func TestIngestInvalidOptionalSubtitlesCanBeOmittedAndRetried(t *testing.T) {
	valid := map[string]any{"id": "zh", "language": "zh-CN", "format": "srt", "content": testSubtitleSRT}
	nine := make([]map[string]any, 9)
	for i := range nine {
		nine[i] = valid
	}
	for _, tc := range []struct {
		name, kind string
		tracks     any
	}{
		{"unknown or other task reference", "video", []any{map[string]any{"id": "zh", "language": "zh-CN", "ref": "subtitle_other_task"}}},
		{"both ref and content", "video", []any{map[string]any{"id": "zh", "language": "zh-CN", "ref": "subtitle_other_task", "content": testSubtitleSRT}}},
		{"duplicate IDs", "video", []any{valid, valid}},
		{"too many tracks", "video", nine},
		{"article subtitles", "article", []any{valid}},
		{"oversize", "video", []any{map[string]any{"id": "zh", "language": "zh-CN", "content": testSubtitleSRT + strings.Repeat("x", maximumSubtitleBytes)}}},
		{"not time-coded captions", "video", []any{map[string]any{"id": "zh", "language": "zh-CN", "content": "made-up summary"}}},
		{"missing language", "video", []any{map[string]any{"id": "zh", "content": testSubtitleSRT}}},
		{"unsupported format", "video", []any{map[string]any{"id": "zh", "language": "zh-CN", "format": "ass", "content": testSubtitleSRT}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte(`{"data":{"created":1,"items":[]}}`))
			}))
			defer remote.Close()
			client, _ := serverapi.New(remote.URL, "wzag-test", "test")
			stats := &serverapi.Stats{}
			ingestor := NewIngestor(client, &serverapi.Task{ID: "task", Constraints: serverapi.Constraints{MaxItems: 1}}, 1, stats, NewSubtitleCollector(true, nil))
			item := map[string]any{"external_id": "video", "kind": tc.kind, "title": "Video", "subtitles": tc.tracks}
			if _, err := ingestor.Handle(context.Background(), subtitleToolCall(t, map[string]any{"items": []any{item}})); err == nil || hits.Load() != 0 || stats.ItemsCreated != 0 {
				t.Fatalf("invalid subtitle was uploaded or consumed quota: %v %+v hits=%d", err, stats, hits.Load())
			}
			delete(item, "subtitles")
			if _, err := ingestor.Handle(context.Background(), subtitleToolCall(t, map[string]any{"items": []any{item}})); err != nil || stats.ItemsCreated != 1 || hits.Load() != 1 {
				t.Fatalf("omitting optional subtitles must allow a retry: %v %+v", err, stats)
			}
		})
	}
}
