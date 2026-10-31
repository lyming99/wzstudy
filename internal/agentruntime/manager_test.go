package agentruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wzstudy/internal/study"
)

func runtimeFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, agentBinaryName()), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "agent.env"), []byte("WZSTUDY_SERVER_URL=http://127.0.0.1:8091\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestValidateWorkDir(t *testing.T) {
	if err := ValidateWorkDir(""); !errors.Is(err, ErrInvalidWorkDir) {
		t.Fatalf("empty path: %v", err)
	}
	if err := ValidateWorkDir(t.TempDir()); !errors.Is(err, ErrInvalidWorkDir) {
		t.Fatalf("directory without agent files: %v", err)
	}
	if err := ValidateWorkDir(runtimeFixture(t)); err != nil {
		t.Fatalf("valid runtime directory: %v", err)
	}
}

func TestManagerConfigurationAndState(t *testing.T) {
	service := study.NewService(study.NewMemoryStore())
	manager := New(service, nil)
	ctx := context.Background()
	if err := manager.Stop(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stop before start: %v", err)
	}
	if err := manager.Start(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("start without config: %v", err)
	}
	directory := runtimeFixture(t)
	workDir := filepath.Join(directory, "work")
	model := ModelSettings{Provider: "deepseek", BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat", APIKey: "sk-test"}
	if err := manager.UpdateSettings(ctx, directory, workDir, true, model); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if !status.Configured || status.InstallDir != directory || status.WorkDir != workDir || !status.AutoStart || status.Running {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.ModelProvider != "deepseek" || status.ModelName != "deepseek-chat" || !status.HasModelAPIKey {
		t.Fatalf("unexpected model status: %+v", status)
	}
	if !filepath.IsAbs(status.DefaultWorkDir) || filepath.Base(status.DefaultWorkDir) != "work" || filepath.Base(filepath.Dir(status.DefaultWorkDir)) != "agent" {
		t.Fatalf("default work dir must be <install>/agent/work: %q", status.DefaultWorkDir)
	}
	if filepath.Base(status.DefaultInstallDir) != "agent" {
		t.Fatalf("default install dir must be <install>/agent: %q", status.DefaultInstallDir)
	}
	if status.Binary != filepath.Join(directory, agentBinaryName()) {
		t.Fatalf("unexpected binary path: %s", status.Binary)
	}
	if err := manager.UpdateSettings(ctx, filepath.Join(directory, "missing"), workDir, false, ModelSettings{}); !errors.Is(err, ErrInvalidInstall) {
		t.Fatalf("invalid install dir must be rejected: %v", err)
	}
	if err := manager.UpdateSettings(ctx, directory, workDir, true, ModelSettings{Provider: "deepseek"}); err != nil {
		t.Fatal(err)
	}
	if !manager.Status().HasModelAPIKey {
		t.Fatal("empty API key must preserve the saved key")
	}
	if err := manager.UpdateSettings(ctx, directory, workDir, true, ModelSettings{Provider: "deepseek", ClearAPIKey: true}); err != nil {
		t.Fatal(err)
	}
	if manager.Status().HasModelAPIKey {
		t.Fatal("clear_api_key must remove the saved key")
	}
	if err := manager.UpdateSettings(ctx, directory, workDir, true, ModelSettings{Provider: "unknown"}); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("unknown provider must be rejected: %v", err)
	}
}
