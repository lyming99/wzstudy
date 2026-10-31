package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"wzstudy/internal/agentruntime"
)

func TestAgentModelConnectionUsesSelectedModelAndSavedKey(t *testing.T) {
	var authorization string
	var called int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		authorization = r.Header.Get("Authorization")
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Stream    bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "selected-model" || payload.MaxTokens != 16 || payload.Stream {
			t.Errorf("invalid payload: %+v", payload)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer upstream.Close()
	handler, manager, _ := runtimeApp(t)
	dir := t.TempDir()
	binary := "wzstudy-agent"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	for name, content := range map[string]string{binary: "placeholder", "agent.env": "AGENT_MODEL=old"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.UpdateSettings(testWorkspaceContext(), dir, filepath.Join(dir, "work"), false, agentruntime.ModelSettings{Provider: "custom", BaseURL: upstream.URL + "/v1", Model: "saved-model", APIKey: "sk-saved-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key string
		clear     bool
		want      string
	}{
		{"saved", "", false, "Bearer sk-saved-secret"}, {"override", "sk-new-secret", false, "Bearer sk-new-secret"}, {"clear", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, handler, "POST", "/api/v1/admin/agent/runtime/test-connection", adminKey, map[string]any{"provider": "custom", "base_url": upstream.URL + "/v1", "model": "selected-model", "api_key": tc.key, "use_saved_key": true, "clear_api_key": tc.clear})
			if w.Code != 200 {
				t.Fatalf("test connection: %d %s", w.Code, w.Body.String())
			}
			if authorization != tc.want {
				t.Error("wrong model credential source")
			}
			if !strings.Contains(w.Body.String(), `"connected":true`) || strings.Contains(w.Body.String(), "sk-") {
				t.Fatal("invalid or secret-bearing result", w.Body.String())
			}
			if manager.SavedModelAPIKey() != "sk-saved-secret" || manager.Settings().ModelName != "saved-model" || manager.Status().Running {
				t.Fatal("connection test changed saved settings or started agent")
			}
		})
	}
	if called != 3 {
		t.Fatal("unexpected call count", called)
	}
}

func TestAgentModelConnectionErrorsAndPermissions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, `{"error":{"message":"sk-private-upstream"}}`},
		{"model_missing", 404, "not found"}, {"rate_limit", 429, "limit"},
		{"invalid_json", 200, "<html>ok</html>"}, {"no_choices", 200, `{"data":[]}`}, {"empty_message", 200, `{"choices":[{"message":{}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			handler, _, _ := runtimeApp(t)
			input := map[string]any{"provider": "custom", "base_url": upstream.URL, "model": "test", "api_key": "sk-private-request"}
			w := request(t, handler, "POST", "/api/v1/admin/agent/runtime/test-connection", adminKey, input)
			if w.Code != 502 {
				t.Fatalf("expected 502: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "sk-private") {
				t.Fatal("leaked secret")
			}
			if tc.status == 401 && !strings.Contains(w.Body.String(), "模型 API Key") {
				t.Fatal("unclear model authentication error")
			}
			for _, key := range []string{"", legacyReadKey} {
				if w := request(t, handler, "POST", "/api/v1/admin/agent/runtime/test-connection", key, input); w.Code != 401 {
					t.Fatal("not admin-only", w.Code)
				}
			}
			input["model"] = ""
			if w := request(t, handler, "POST", "/api/v1/admin/agent/runtime/test-connection", adminKey, input); w.Code != 400 {
				t.Fatal("empty model accepted", w.Code)
			}
		})
	}
}

func TestAgentModelConnectionDoesNotFollowRedirect(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	handler, _, _ := runtimeApp(t)
	w := request(t, handler, "POST", "/api/v1/admin/agent/runtime/test-connection", adminKey, map[string]any{"provider": "custom", "base_url": redirect.URL, "model": "test", "api_key": "sk-secret"})
	if w.Code != 502 || reached {
		t.Fatal("redirect followed or incorrectly accepted")
	}
}
