package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentSaveAndRestartReportsPartialFailure(t *testing.T) {
	handler, manager, _ := runtimeApp(t)
	dir := t.TempDir()
	binary := "wzstudy-agent"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	for name, content := range map[string]string{binary: "placeholder", "agent.env": "AGENT_MODEL=old"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	input := map[string]any{"install_dir": dir, "work_dir": filepath.Join(dir, "work"), "auto_start": true, "restart": true, "model": map[string]any{"provider": "custom", "model": "new-model", "base_url": "https://model.example/v1", "api_key": "sk-do-not-leak"}}
	w := request(t, handler, http.MethodPut, "/api/v1/admin/agent/runtime", adminKey, input)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "配置已保存") {
		t.Fatalf("partial failure not reported: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-do-not-leak") {
		t.Fatal("secret leaked")
	}
	if manager.Settings().ModelName != "new-model" || manager.SavedModelAPIKey() != "sk-do-not-leak" || manager.Status().Running {
		t.Fatal("unexpected partial failure state")
	}
	w = request(t, handler, http.MethodPost, "/api/v1/admin/agent/runtime/restart", adminKey, map[string]any{})
	if w.Code != 502 {
		t.Fatal("placeholder restart incorrectly succeeded", w.Code)
	}
}
