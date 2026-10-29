package loop

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"wzstudy-agent/internal/collect"
	"wzstudy-agent/internal/provider"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/skill"
	"wzstudy-agent/internal/tool"
)

type scriptedProvider struct {
	responses []provider.Response
	calls     int
}

func (p *scriptedProvider) Chat(context.Context, string, []provider.Message, []provider.ToolSchema) (provider.Response, error) {
	if p.calls >= len(p.responses) {
		return p.responses[len(p.responses)-1], nil
	}
	response := p.responses[p.calls]
	p.calls++
	return response, nil
}

type rotatingProvider struct{ round int }

func (p *rotatingProvider) Chat(context.Context, string, []provider.Message, []provider.ToolSchema) (provider.Response, error) {
	p.round++
	return provider.Response{ToolCalls: []provider.ToolCall{{
		ID:        fmt.Sprintf("call-%d", p.round),
		Name:      "echo",
		Arguments: fmt.Sprintf(`{"n":%d}`, p.round),
	}}}, nil
}

func fixtureTask(rounds, toolCalls int) *serverapi.Task {
	return &serverapi.Task{
		ID: "task-1", SourceID: "source-1", Goal: "采集示例文章", Attempts: 1,
		Constraints: serverapi.Constraints{MaxItems: 5},
		Budget:      serverapi.Budget{MaxRounds: rounds, MaxToolCalls: toolCalls, TimeoutSeconds: 60},
	}
}

func newRunner(t *testing.T, script provider.Provider) (*Runner, *[]serverapi.EventInput) {
	t.Helper()
	events := &[]serverapi.EventInput{}
	runner := &Runner{
		Provider: script,
		Model:    "test-model",
		Skills:   skill.Load(t.TempDir()),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildTools: func(_ *serverapi.Task, _ int, control *tool.FinishControl, _ *serverapi.Stats) *tool.Registry {
			registry := tool.NewRegistry()
			registry.Register(tool.Definition{Name: "echo", Description: "echo", Parameters: tool.Object(nil)}, func(context.Context, tool.Call) (tool.Result, error) {
				return tool.Result{Content: "ok", Summary: "echo done"}, nil
			})
			registry.Register(collect.FinishDefinition(), collect.FinishHandler(control))
			return registry
		},
	}
	return runner, events
}

func collectEvents(target *[]serverapi.EventInput) func([]serverapi.EventInput) error {
	return func(batch []serverapi.EventInput) error {
		*target = append(*target, batch...)
		return nil
	}
}

func TestLoopSucceedsAfterToolRound(t *testing.T) {
	script := &scriptedProvider{responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "echo", Arguments: `{}`}}},
		{Text: "采集完成，共 1 条。"},
	}}
	runner, events := newRunner(t, script)
	outcome := runner.Run(context.Background(), fixtureTask(4, 8), collectEvents(events))
	if outcome.Status != serverapi.StatusSucceeded || !strings.Contains(outcome.Summary, "采集完成") {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if outcome.Stats.Rounds != 2 || outcome.Stats.ToolCalls != 1 {
		t.Fatalf("unexpected stats: %+v", outcome.Stats)
	}
	if len(*events) == 0 {
		t.Fatal("expected progress events")
	}
	kinds := map[string]bool{}
	for _, event := range *events {
		kinds[event.Kind] = true
	}
	for _, kind := range []string{"round_start", "tool_call", "tool_result", "finish"} {
		if !kinds[kind] {
			t.Fatalf("missing %s event: %+v", kind, *events)
		}
	}
}

func TestLoopStopsAtRoundLimit(t *testing.T) {
	runner, events := newRunner(t, &rotatingProvider{})
	outcome := runner.Run(context.Background(), fixtureTask(3, 50), collectEvents(events))
	if outcome.Status != serverapi.StatusFailed || outcome.Error == nil || outcome.Error.Code != "round_limit" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if outcome.Stats.Rounds != 3 {
		t.Fatalf("unexpected rounds: %+v", outcome.Stats)
	}
}

func TestLoopStopsAtToolCallLimit(t *testing.T) {
	script := &scriptedProvider{responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "echo", Arguments: `{"n":1}`},
			{ID: "c2", Name: "echo", Arguments: `{"n":2}`},
		}},
	}}
	runner, events := newRunner(t, script)
	outcome := runner.Run(context.Background(), fixtureTask(4, 1), collectEvents(events))
	if outcome.Status != serverapi.StatusFailed || outcome.Error == nil || outcome.Error.Code != "tool_call_limit" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestLoopStopsRepeatingIdenticalCalls(t *testing.T) {
	script := &scriptedProvider{responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "echo", Arguments: `{}`}}},
	}}
	runner, events := newRunner(t, script)
	outcome := runner.Run(context.Background(), fixtureTask(12, 50), collectEvents(events))
	if outcome.Status != serverapi.StatusFailed || outcome.Error == nil || outcome.Error.Code != "no_progress" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestLoopHonorsExplicitFinish(t *testing.T) {
	script := &scriptedProvider{responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "finish_task", Arguments: `{"summary":"配额内已无更多内容"}`}}},
	}}
	runner, events := newRunner(t, script)
	outcome := runner.Run(context.Background(), fixtureTask(4, 8), collectEvents(events))
	if outcome.Status != serverapi.StatusSucceeded || outcome.Summary != "配额内已无更多内容" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestLoopStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner, events := newRunner(t, &rotatingProvider{})
	outcome := runner.Run(ctx, fixtureTask(4, 8), collectEvents(events))
	if outcome.Status != serverapi.StatusCancelled {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}
