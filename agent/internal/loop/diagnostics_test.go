package loop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"wzstudy-agent/internal/provider"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

func TestToolErrorDetailsAndFinalCallCountAreReported(t *testing.T) {
	script := &scriptedProvider{responses: []provider.Response{{ToolCalls: []provider.ToolCall{{ID: "1", Name: "broken", Arguments: `{}`}}}, {Text: "cannot continue"}}}
	runner, events := newRunner(t, script)
	runner.BuildTools = func(_ *serverapi.Task, _ int, _ *tool.FinishControl, _ *serverapi.Stats) *tool.Registry {
		r := tool.NewRegistry()
		r.Register(tool.Definition{Name: "broken"}, func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{}, errors.New("server 409 conflict: previously deleted")
		})
		return r
	}
	result := runner.Run(context.Background(), fixtureTask(3, 3), collectEvents(events))
	if result.Stats.ToolCalls != 1 {
		t.Fatal("call count lost", result.Stats)
	}
	found := false
	for _, e := range *events {
		if e.Kind == "tool_result" && strings.Contains(e.Text, "409 conflict") {
			found = true
		}
	}
	if !found {
		t.Fatal("concrete tool error hidden", *events)
	}
}

func TestDeadlineIsFailureAndCancellationRemainsCancellation(t *testing.T) {
	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	runner, events := newRunner(t, &rotatingProvider{})
	result := runner.Run(deadline, fixtureTask(3, 3), collectEvents(events))
	if result.Status != serverapi.StatusFailed || result.Error == nil || result.Error.Code != "timeout" || strings.Contains(result.Summary, "已取消") {
		t.Fatalf("wrong deadline outcome %+v", result)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	result = runner.Run(canceled, fixtureTask(3, 3), collectEvents(events))
	if result.Status != serverapi.StatusCancelled || result.Error != nil {
		t.Fatalf("wrong cancellation outcome %+v", result)
	}
}

func TestClipAlwaysPreservesUTF8(t *testing.T) {
	text := "中文错误：工具调用失败"
	for n := 0; n < len(text); n++ {
		out := clip(text, n)
		if !utf8.ValidString(out) || len(out) > n {
			t.Fatalf("invalid UTF-8 at %d: %q", n, out)
		}
	}
}
