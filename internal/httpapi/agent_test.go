package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

func agentApp(t *testing.T) (*study.Service, http.Handler, study.Source, study.Agent, string, study.CollectionTask) {
	t.Helper()
	service := study.NewService(study.NewMemoryStore())
	seedTestWorkspace(service)
	cfg := config.Config{Env: "test", AdminKey: adminKey, ReadKey: legacyReadKey, AgentEnabled: true, AgentLeaseSeconds: 300, CORSOrigins: []string{"https://study.example"}}
	ctx := testWorkspaceContext()
	source, _, err := service.CreateSource(ctx, study.SourceInput{Slug: "agent-lessons", Name: "Agent Lessons"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	agent, key, err := service.CreateAgent(ctx, study.AgentInput{Slug: "collector", Name: "Collector"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	task, err := service.CreateCollectionTask(ctx, study.CollectionTaskInput{SourceID: source.ID, Goal: "采集示例站点文章", Constraints: study.TaskConstraints{MaxItems: 5}})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	handler := New(cfg, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return service, handler, source, agent, key, task
}

func TestAgentEndpointsAreHiddenWhenDisabled(t *testing.T) {
	_, handler := app()
	w := request(t, handler, "POST", "/api/v1/agent/tasks/claim", "wzag_disabled", map[string]any{})
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled agent feature must be a missing route, got %d %s", w.Code, w.Body.String())
	}
}

func TestAgentProtocolRoundTrip(t *testing.T) {
	service, handler, source, _, key, task := agentApp(t)

	if w := request(t, handler, "POST", "/api/v1/agent/tasks/claim", "", map[string]any{}); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous claim: %d", w.Code)
	}
	if w := request(t, handler, "POST", "/api/v1/agent/tasks/claim", "wzag_invalid", map[string]any{}); w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid agent key: %d", w.Code)
	}

	var claimed struct {
		Task study.CollectionTask `json:"task"`
	}
	data(t, request(t, handler, "POST", "/api/v1/agent/tasks/claim", key, map[string]any{"capacity": 1, "version": "1.0.0", "hostname": "ci"}), &claimed)
	if claimed.Task.ID != task.ID || claimed.Task.Attempts != 1 || claimed.Task.Status != study.TaskStatusClaimed {
		t.Fatalf("unexpected claim: %+v", claimed.Task)
	}
	path := "/api/v1/agent/tasks/" + task.ID

	var heartbeat struct {
		CancelRequested bool   `json:"cancel_requested"`
		Status          string `json:"status"`
	}
	data(t, request(t, handler, "POST", path+"/heartbeat", key, map[string]any{}), &heartbeat)
	if heartbeat.CancelRequested || heartbeat.Status != study.TaskStatusRunning {
		t.Fatalf("unexpected heartbeat: %+v", heartbeat)
	}

	var events struct {
		Accepted   int `json:"accepted"`
		HighestSeq int `json:"highest_seq"`
	}
	data(t, request(t, handler, "POST", path+"/events", key, map[string]any{"attempt": 1, "events": []map[string]any{
		{"seq": 1, "kind": "assistant", "text": "开始采集"},
		{"seq": 2, "kind": "tool_call", "tool": "fetch_url", "summary": "抓取示例页"},
	}}), &events)
	if events.Accepted != 2 || events.HighestSeq != 2 {
		t.Fatalf("unexpected events ack: %+v", events)
	}

	var receipt study.IntakeReceipt
	data(t, request(t, handler, "POST", path+"/items", key, map[string]any{"attempt": 1, "items": []study.Content{
		{ExternalID: "a1", Title: "A1", Format: "markdown", Content: "body", URL: "https://example.com/a1"},
	}}), &receipt)
	if receipt.Created != 1 || receipt.TaskID != task.ID || receipt.Integration != "agent" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	if w := request(t, handler, "POST", path+"/complete", key, map[string]any{
		"attempt": 1, "status": "succeeded", "summary": "完成", "stats": map[string]any{"rounds": 2, "tool_calls": 3},
	}); w.Code != http.StatusNoContent {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}

	stored, err := service.CollectionTask(testWorkspaceContext(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != study.TaskStatusSucceeded || stored.Stats.Rounds != 2 {
		t.Fatalf("unexpected stored task: %+v", stored)
	}

	if w := request(t, handler, "POST", "/api/v1/ingest/"+source.ID, key, map[string]any{"items": []study.Content{{ExternalID: "x", Title: "X"}}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("agent key must not act as a source key: %d", w.Code)
	}
	if w := request(t, handler, "GET", "/api/v1/admin/agent/tasks", key, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("agent key must not manage tasks: %d", w.Code)
	}
}

func TestAgentEventsRejectInvalidKind(t *testing.T) {
	_, handler, _, _, key, task := agentApp(t)
	data(t, request(t, handler, "POST", "/api/v1/agent/tasks/claim", key, map[string]any{}), &struct {
		Task study.CollectionTask `json:"task"`
	}{})
	path := "/api/v1/agent/tasks/" + task.ID
	if w := request(t, handler, "POST", path+"/events", key, map[string]any{"attempt": 1, "events": []map[string]any{{"seq": 1, "kind": "shell"}}}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown event kind must be rejected: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, "POST", path+"/items", key, map[string]any{"attempt": 1, "items": []study.Content{{ExternalID: "a1", Title: "A1"}}}); w.Code != http.StatusOK {
		t.Fatalf("items without allowlist must pass: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentCancelSignalReachesAgent(t *testing.T) {
	service, handler, _, _, key, task := agentApp(t)
	data(t, request(t, handler, "POST", "/api/v1/agent/tasks/claim", key, map[string]any{}), &struct {
		Task study.CollectionTask `json:"task"`
	}{})
	if _, err := service.CancelAgentTask(testWorkspaceContext(), task.ID, "管理员取消"); err != nil {
		t.Fatal(err)
	}
	var heartbeat struct {
		CancelRequested bool   `json:"cancel_requested"`
		CancelReason    string `json:"cancel_reason"`
	}
	data(t, request(t, handler, "POST", "/api/v1/agent/tasks/"+task.ID+"/heartbeat", key, map[string]any{}), &heartbeat)
	if !heartbeat.CancelRequested || heartbeat.CancelReason != "管理员取消" {
		t.Fatalf("unexpected cancel signal: %+v", heartbeat)
	}
	if w := request(t, handler, "POST", "/api/v1/agent/tasks/"+task.ID+"/items", key, map[string]any{"attempt": 1, "items": []study.Content{{ExternalID: "c1", Title: "C1"}}}); w.Code != http.StatusConflict {
		t.Fatalf("cancelled task must reject items: %d %s", w.Code, w.Body.String())
	}
}
