package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wzstudy/internal/agentruntime"
	"wzstudy/internal/config"
)

func runtimeApp(t *testing.T) (http.Handler, *agentruntime.Manager, string) {
	t.Helper()
	service, _, dir := managementApp(t, 1<<20)
	manager := agentruntime.New(service, nil)
	cfg := config.Config{Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: legacyReadKey, DataDir: dir, MaxUploadBytes: 1 << 20}
	handler := New(cfg, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), WithAgentRuntime(manager))
	return handler, manager, dir
}

func TestAgentRuntimeAdminOnly(t *testing.T) {
	handler, _, _ := runtimeApp(t)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/agent/runtime"},
		{http.MethodPost, "/api/v1/admin/agent/deployments"},
		{http.MethodPost, "/api/v1/admin/agent/models"},
		{http.MethodPost, "/api/v1/admin/agent/runtime/test-connection"},
		{http.MethodPost, "/api/v1/admin/agent/runtime/restart"},
	}
	for _, item := range cases {
		if w := request(t, handler, item.method, item.path, legacyReadKey, map[string]any{}); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s must be administrator-only: %d", item.path, w.Code)
		}
	}
}

func TestAgentRuntimeRequiresManager(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/runtime", adminKey, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("runtime without manager: %d", w.Code)
	}
}

func TestAgentRuntimeConfigureAndStart(t *testing.T) {
	handler, _, _ := runtimeApp(t)
	var initial struct {
		Configured        bool   `json:"configured"`
		DefaultInstallDir string `json:"default_install_dir"`
		DefaultWorkDir    string `json:"default_work_dir"`
	}
	data(t, request(t, handler, http.MethodGet, "/api/v1/admin/agent/runtime", adminKey, nil), &initial)
	if initial.Configured {
		t.Fatal("runtime must start unconfigured")
	}
	if filepath.Base(initial.DefaultInstallDir) != "agent" || filepath.Base(initial.DefaultWorkDir) != "work" {
		t.Fatalf("unexpected defaults: %q %q", initial.DefaultInstallDir, initial.DefaultWorkDir)
	}

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "wzstudy-agent.exe"), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "agent.env"), []byte("AGENT_MODEL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var configured struct {
		Configured     bool   `json:"configured"`
		InstallDir     string `json:"install_dir"`
		WorkDir        string `json:"work_dir"`
		ModelProvider  string `json:"model_provider"`
		ModelName      string `json:"model_name"`
		HasModelAPIKey bool   `json:"has_model_api_key"`
	}
	data(t, request(t, handler, http.MethodPut, "/api/v1/admin/agent/runtime", adminKey, map[string]any{
		"install_dir": directory, "work_dir": filepath.Join(directory, "work"), "auto_start": true,
		"model": map[string]any{"provider": "deepseek", "base_url": "https://api.deepseek.com/v1", "model": "deepseek-chat", "api_key": "sk-test"},
	}), &configured)
	if !configured.Configured || configured.InstallDir != directory || configured.WorkDir != filepath.Join(directory, "work") {
		t.Fatalf("unexpected configured status: %+v", configured)
	}
	if configured.ModelProvider != "deepseek" || configured.ModelName != "deepseek-chat" || !configured.HasModelAPIKey {
		t.Fatalf("unexpected model status: %+v", configured)
	}
	if strings.Contains(configured.ModelName, "sk-test") {
		t.Fatal("API key must never be echoed")
	}

	if w := request(t, handler, http.MethodPut, "/api/v1/admin/agent/runtime", adminKey, map[string]any{
		"install_dir": filepath.Join(directory, "missing"), "work_dir": filepath.Join(directory, "work"),
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid install dir must be rejected: %d", w.Code)
	}

	started := request(t, handler, http.MethodPost, "/api/v1/admin/agent/runtime/start", adminKey, map[string]any{})
	if started.Code != http.StatusInternalServerError {
		t.Fatalf("placeholder binary must fail to execute: %d %s", started.Code, started.Body.String())
	}
}
