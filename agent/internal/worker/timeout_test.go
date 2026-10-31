package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"wzstudy-agent/internal/loop"
	"wzstudy-agent/internal/provider"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/skill"
	"wzstudy-agent/internal/tool"
)

type slowToolProvider struct{}

func (slowToolProvider) Chat(context.Context, string, []provider.Message, []provider.ToolSchema) (provider.Response, error) {
	return provider.Response{ToolCalls: []provider.ToolCall{{ID: "slow-1", Name: "slow", Arguments: `{}`}}}, nil
}

func TestTimeoutReportsTailEventsAndConsistentPartialOutcome(t *testing.T) {
	eventsReported := false
	var reported struct {
		Status  string               `json:"status"`
		Summary string               `json:"summary"`
		Stats   serverapi.Stats      `json:"stats"`
		Error   *serverapi.TaskError `json:"error"`
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			eventsReported = true
			_, _ = w.Write([]byte(`{"data":{"accepted":3,"highest_seq":3}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/complete") {
			if err := json.NewDecoder(r.Body).Decode(&reported); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		t.Error("unexpected endpoint", r.URL.Path)
	}))
	defer remote.Close()
	client, _ := serverapi.New(remote.URL, "wzag-test", "test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := &loop.Runner{Provider: slowToolProvider{}, Skills: skill.Load(t.TempDir()), Logger: logger, BuildTools: func(_ *serverapi.Task, _ int, _ *tool.FinishControl, stats *serverapi.Stats) *tool.Registry {
		stats.ItemsCreated = 2
		registry := tool.NewRegistry()
		registry.Register(tool.Definition{Name: "slow"}, func(ctx context.Context, _ tool.Call) (tool.Result, error) {
			<-ctx.Done()
			return tool.Result{}, ctx.Err()
		})
		return registry
	}}
	worker := &Worker{Client: client, Runner: runner, Logger: logger}
	deadline := time.Now().Add(100 * time.Millisecond)
	worker.execute(context.Background(), &serverapi.Task{ID: "task", Attempts: 1, Deadline: &deadline, Budget: serverapi.Budget{MaxRounds: 2, MaxToolCalls: 2}})
	if !eventsReported {
		t.Fatal("tail events dropped at timeout")
	}
	if reported.Status != "failed" || reported.Error == nil || reported.Error.Code != "timeout" || strings.Contains(reported.Summary, "已取消") {
		t.Fatalf("inconsistent timeout outcome %+v", reported)
	}
	if reported.Stats.ItemsCreated != 2 || reported.Stats.ToolCalls != 1 {
		t.Fatal("partial stats lost", reported.Stats)
	}
}
