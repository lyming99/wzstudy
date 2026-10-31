package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentModelsListsOpenAICompatibleCatalog(t *testing.T) {
	var authorization string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"},{"id":"model-a"}]}`))
	}))
	defer modelServer.Close()
	_, handler, _ := managementApp(t, 1<<20)
	response := request(t, handler, http.MethodPost, "/api/v1/admin/agent/models", adminKey, map[string]any{
		"provider": "custom",
		"base_url": modelServer.URL,
		"api_key":  "sk-test",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("model list failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			Models []string `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Models) != 2 || payload.Data.Models[0] != "model-a" || payload.Data.Models[1] != "model-b" {
		t.Fatalf("unexpected models: %+v", payload.Data.Models)
	}
	if authorization != "Bearer sk-test" {
		t.Fatalf("model request must forward the key: %q", authorization)
	}
}

func TestAgentModelsRejectsInvalidInputAndUpstreamErrors(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/models", adminKey, map[string]any{"provider": "unknown"}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider: %d", w.Code)
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/models", adminKey, map[string]any{
		"provider": "custom", "base_url": "ftp://example.com",
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("non-http base url: %d", w.Code)
	}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer modelServer.Close()
	response := request(t, handler, http.MethodPost, "/api/v1/admin/agent/models", adminKey, map[string]any{
		"provider": "custom", "base_url": modelServer.URL, "api_key": "bad",
	})
	if response.Code != http.StatusBadGateway {
		t.Fatalf("upstream error must be 502: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "bad key") {
		t.Fatal("upstream body must not be echoed verbatim")
	}
}
