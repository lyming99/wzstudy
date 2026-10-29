package serverapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaimParsesTaskAndAuthenticates(t *testing.T) {
	var authorization, path, method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization, path, method = r.Header.Get("Authorization"), r.URL.Path, r.Method
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"task": map[string]any{
			"id": "task-1", "source_id": "source-1", "goal": "采集", "attempts": 1,
			"constraints": map[string]any{"max_items": 3},
			"budget":      map[string]any{"max_rounds": 4, "max_tool_calls": 8, "timeout_seconds": 60},
		}}})
	}))
	defer server.Close()
	client, err := New(server.URL, "wzag_test", "test")
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.Claim(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer wzag_test" || path != "/api/v1/agent/tasks/claim" || method != http.MethodPost {
		t.Fatalf("unexpected request: %s %s %s", authorization, method, path)
	}
	if task == nil || task.ID != "task-1" || task.Attempts != 1 || task.Constraints.MaxItems != 3 || task.Budget.MaxRounds != 4 {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestClaimReturnsNilOnNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, _ := New(server.URL, "wzag_test", "test")
	task, err := client.Claim(context.Background(), 1, 0)
	if err != nil || task != nil {
		t.Fatalf("204 must be an empty claim: task=%v err=%v", task, err)
	}
}

func TestErrorContractIsParsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"lease_expired","message":"租约已过期"}}`))
	}))
	defer server.Close()
	client, _ := New(server.URL, "wzag_test", "test")
	_, err := client.Heartbeat(context.Background(), "task-1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsStatus(err, http.StatusConflict) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompleteSendsStatsAndAcceptsNoContent(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/tasks/task-1/complete" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, _ := New(server.URL, "wzag_test", "test")
	if err := client.Complete(context.Background(), "task-1", 2, StatusSucceeded, "完成", Stats{Rounds: 3, ToolCalls: 5}, nil); err != nil {
		t.Fatal(err)
	}
	if payload["attempt"].(float64) != 2 || payload["status"] != StatusSucceeded {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	stats, _ := payload["stats"].(map[string]any)
	if stats["rounds"].(float64) != 3 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
