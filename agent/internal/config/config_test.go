package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.env")
	body := "# 注释\nWZSTUDY_SERVER_URL=http://127.0.0.1:8091\nAGENT_MODEL=\"model-x\"\n\nAGENT_MODEL_API_KEY='secret'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_CONFIG", path)
	values, err := loadConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if values["WZSTUDY_SERVER_URL"] != "http://127.0.0.1:8091" || values["AGENT_MODEL"] != "model-x" || values["AGENT_MODEL_API_KEY"] != "secret" {
		t.Fatalf("unexpected config values: %+v", values)
	}
}

func TestEnvironmentOverridesConfigFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.env")
	body := "WZSTUDY_SERVER_URL=http://from-file\nWZSTUDY_AGENT_KEY=wzag_file\nAGENT_MODEL=file-model\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_CONFIG", path)
	t.Setenv("WZSTUDY_SERVER_URL", "http://from-env")
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ServerURL != "http://from-env" || loaded.Model != "file-model" {
		t.Fatalf("environment must win over the file: %+v", loaded)
	}
}

func TestMissingConfigFileIsReported(t *testing.T) {
	t.Setenv("AGENT_CONFIG", filepath.Join(t.TempDir(), "absent.env"))
	if _, err := Load(); err == nil {
		t.Fatal("missing AGENT_CONFIG must be reported instead of silently ignored")
	}
}
