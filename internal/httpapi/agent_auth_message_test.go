package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestAgentInvalidKeyMessage(t *testing.T) {
	service, handler, _, agent, oldKey, _ := agentApp(t)
	_, newKey, err := service.RotateAgentKey(testWorkspaceContext(), agent.ID, agent.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"wzag_invalid", oldKey} {
		for _, path := range []string{"/api/v1/agent/status", "/api/v1/agent/tasks/claim"} {
			method := http.MethodGet
			var body any
			if strings.HasSuffix(path, "/claim") {
				method = http.MethodPost
				body = map[string]any{}
			}
			w := request(t, handler, method, path, key, body)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
			}
			for _, text := range []string{"unauthorized", "采集端身份或密钥无效", "WZSTUDY_AGENT_KEY", "不是模型 API Key"} {
				if !strings.Contains(w.Body.String(), text) {
					t.Fatalf("missing %q: %s", text, w.Body.String())
				}
			}
			if strings.Contains(w.Body.String(), "数据源密钥") || strings.Contains(w.Body.String(), key) {
				t.Fatal("misleading message or leaked key")
			}
		}
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/agent/status", newKey, nil); w.Code != http.StatusOK {
		t.Fatalf("new key rejected: %d", w.Code)
	}
}
