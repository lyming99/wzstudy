package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatSendsToolsAndParsesToolCalls(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing bearer credential")
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"先抓取","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"fetch_url","arguments":"{\"url\":\"https://example.com\"}"}}
			]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5}
		}`))
	}))
	defer server.Close()
	client, err := NewOpenAI(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "task"}}
	tools := []ToolSchema{{Name: "fetch_url", Description: "fetch", Parameters: map[string]any{"type": "object"}}}
	response, err := client.Chat(context.Background(), "model-x", messages, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "fetch_url" || !strings.Contains(response.ToolCalls[0].Arguments, "example.com") {
		t.Fatalf("unexpected tool calls: %+v", response.ToolCalls)
	}
	if response.Text != "先抓取" || response.Usage.PromptTokens != 10 {
		t.Fatalf("unexpected response: %+v", response)
	}
	definitions, _ := request["tools"].([]any)
	if len(definitions) != 1 {
		t.Fatalf("tools were not sent: %+v", request["tools"])
	}
	if request["tool_choice"] != "auto" {
		t.Fatalf("tool_choice missing: %+v", request["tool_choice"])
	}
}

func TestChatRejectsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()
	client, _ := NewOpenAI(server.URL, "")
	_, err := client.Chat(context.Background(), "model", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolMessageReplayShape(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&request)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}]}`))
	}))
	defer server.Close()
	client, _ := NewOpenAI(server.URL, "")
	messages := []Message{
		{Role: RoleAssistant, Content: "calling", ToolCalls: []ToolCall{{ID: "c1", Name: "fetch_url", Arguments: "{}"}}},
		{Role: RoleTool, Content: "result", ToolCallID: "c1"},
	}
	if _, err := client.Chat(context.Background(), "m", messages, nil); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(request["messages"])
	text := string(encoded)
	if !strings.Contains(text, `"tool_call_id":"c1"`) || !strings.Contains(text, `"tool_calls"`) {
		t.Fatalf("tool replay shape is wrong: %s", text)
	}
}
