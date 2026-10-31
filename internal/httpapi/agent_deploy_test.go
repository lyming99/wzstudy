package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/agentbundle"
	"wzstudy/internal/study"
)

func TestAgentDeploymentReportsMissingBundle(t *testing.T) {
	if agentbundle.Available() {
		t.Skip("this build embeds the agent binary")
	}
	_, handler, _ := managementApp(t, 1<<20)
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/binary", adminKey, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("binary without bundle: %d", w.Code)
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/deployments", adminKey, map[string]any{"name": "本机采集端"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("deployment without bundle: %d", w.Code)
	}
}

func TestAgentDeploymentCreatesReadyArchive(t *testing.T) {
	if !agentbundle.Available() {
		t.Skip("this build does not embed the agent binary")
	}
	service, handler, _ := managementApp(t, 1<<20)
	response := request(t, handler, http.MethodPost, "/api/v1/admin/agent/deployments", adminKey, map[string]any{
		"model": map[string]any{"provider": "openai", "api_key": "sk-test-key"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("deployment failed: %d %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range reader.File {
		handle, _ := entry.Open()
		content, _ := io.ReadAll(handle)
		handle.Close()
		files[entry.Name] = content
	}
	for _, name := range []string{"wzstudy-agent.exe", "agent.env", "start-agent.cmd", "AGENT_README.md"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("deployment archive is missing %s", name)
		}
	}
	if !strings.Contains(string(files["agent.env"]), "WZSTUDY_AGENT_KEY=wzag_") {
		t.Fatal("agent.env must contain the generated key")
	}
	for _, expected := range []string{"AGENT_MODEL_BASE_URL=https://api.openai.com/v1", "AGENT_MODEL_API_KEY=sk-test-key", "AGENT_MODEL=gpt-4o-mini"} {
		if !strings.Contains(string(files["agent.env"]), expected) {
			t.Fatalf("agent.env is missing model setting %s", expected)
		}
	}
	administrator, err := service.Administrator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.Agents(study.WithWorkspace(context.Background(), administrator.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !strings.HasPrefix(list[0].Name, "采集端 ") {
		t.Fatalf("agent identity was not created with an automatic name: %+v", list)
	}
}

func TestAgentDeploymentRejectsInvalidModel(t *testing.T) {
	if !agentbundle.Available() {
		t.Skip("this build does not embed the agent binary")
	}
	_, handler, _ := managementApp(t, 1<<20)
	cases := []map[string]any{
		{"model": map[string]any{"provider": "unknown"}},
		{"model": map[string]any{"provider": "custom", "model": "m", "base_url": "ftp://example.com"}},
		{"model": map[string]any{"provider": "deepseek", "model": strings.Repeat("x", 121)}},
	}
	for index, body := range cases {
		if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/deployments", adminKey, body); w.Code != http.StatusBadRequest {
			t.Fatalf("case %d must be rejected: %d %s", index, w.Code, w.Body.String())
		}
	}
}

func TestAgentBinaryRequiresAdministrator(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/binary", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous binary download must be rejected: %d", w.Code)
	}
}
