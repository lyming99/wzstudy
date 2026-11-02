package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wzstudy-agent/internal/collect"
	"wzstudy-agent/internal/provider"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

func TestLoopSubtitleFailureDoesNotBlockMediaIngest(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var uploads atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/captions.srt" {
					w.WriteHeader(status)
					_, _ = w.Write([]byte("<html>not a subtitle track</html>"))
					return
				}
				var in struct {
					Items []serverapi.Content `json:"items"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Items) != 1 || len(in.Items[0].Subtitles) != 0 {
					t.Errorf("unexpected media upload: %+v %v", in, err)
				}
				uploads.Add(1)
				_, _ = w.Write([]byte(`{"data":{"created":1,"items":[]}}`))
			}))
			defer remote.Close()
			client, _ := serverapi.New(remote.URL, "wzag-test", "test")
			fetchArgs, _ := json.Marshal(map[string]any{"url": remote.URL + "/captions.srt"})
			runner, events := newRunner(t, &scriptedProvider{responses: []provider.Response{
				{ToolCalls: []provider.ToolCall{{ID: "fetch", Name: "fetch_subtitle", Arguments: string(fetchArgs)}}},
				{ToolCalls: []provider.ToolCall{{ID: "ingest", Name: "ingest_items", Arguments: `{"items":[{"external_id":"video","title":"Video","kind":"video","assets":[{"id":"asset"}]}]}`}}},
				{ToolCalls: []provider.ToolCall{{ID: "finish", Name: "finish_task", Arguments: `{"summary":"媒体已入库，字幕不可用，按需跳过"}`}}},
			}})
			runner.BuildTools = func(task *serverapi.Task, attempt int, control *tool.FinishControl, stats *serverapi.Stats) *tool.Registry {
				registry := tool.NewRegistry()
				collector := collect.NewSubtitleCollector(true, task.Constraints.AllowDomains)
				registry.Register(collector.Definition(), collector.Handle)
				ingestor := collect.NewIngestor(client, task, attempt, stats, collector)
				registry.Register(ingestor.Definition(), ingestor.Handle)
				registry.Register(collect.FinishDefinition(), collect.FinishHandler(control))
				return registry
			}
			task := fixtureTask(5, 8)
			task.Constraints.ContentType = "video"
			prompt := runner.systemPrompt(task)
			if !strings.Contains(prompt, "自主决定是否获取及上传") || !strings.Contains(prompt, "省略 subtitles 就不上传") || !strings.Contains(prompt, "不应导致整个媒体任务失败") {
				t.Fatal("AI autonomy or optional-failure guidance is missing")
			}
			outcome := runner.Run(context.Background(), task, collectEvents(events))
			if outcome.Status != serverapi.StatusSucceeded || outcome.Stats.ItemsCreated != 1 || uploads.Load() != 1 || outcome.Stats.ToolCalls != 3 {
				t.Fatalf("optional subtitle failure blocked a media task: %+v uploads=%d", outcome, uploads.Load())
			}
		})
	}
}
