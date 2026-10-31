package agentbundle

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBuildWithProducesDeploymentArchive(t *testing.T) {
	archive, filename, err := BuildWith([]byte("fake-agent-binary"), "http://127.0.0.1:8091", "wzag_test_key", "本机采集端", ModelConfig{
		BaseURL: "https://api.example.com/v1",
		APIKey:  "sk-test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("build deployment: %v", err)
	}
	if !strings.HasSuffix(filename, ".zip") {
		t.Fatalf("unexpected filename: %s", filename)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range reader.File {
		handle, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = content
	}
	expected := []string{"wzstudy-agent.exe", "agent.env", "start-agent.cmd", "start-agent.ps1", "AGENT_README.md", "skills/web-article/SKILL.md"}
	for _, name := range expected {
		if _, ok := files[name]; !ok {
			t.Fatalf("deployment archive is missing %s", name)
		}
	}
	if string(files["wzstudy-agent.exe"]) != "fake-agent-binary" {
		t.Fatal("agent binary was not copied verbatim")
	}
	environment := string(files["agent.env"])
	if !strings.Contains(environment, "WZSTUDY_SERVER_URL=http://127.0.0.1:8091") || !strings.Contains(environment, "WZSTUDY_AGENT_KEY=wzag_test_key") {
		t.Fatalf("agent.env is missing connection settings: %s", environment)
	}
	for _, expected := range []string{"AGENT_MODEL_BASE_URL=https://api.example.com/v1", "AGENT_MODEL_API_KEY=sk-test-key", "AGENT_MODEL=test-model"} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("agent.env is missing model setting %s", expected)
		}
	}
}

func TestBuildWithRejectsInvalidInput(t *testing.T) {
	if _, _, err := BuildWith(nil, "http://127.0.0.1:8091", "wzag_key", "name", ModelConfig{}); err == nil {
		t.Fatal("empty binary must be rejected")
	}
	if _, _, err := BuildWith([]byte("x"), "http://127.0.0.1:8091", "bad-key", "name", ModelConfig{}); err == nil {
		t.Fatal("non-agent key must be rejected")
	}
	if _, _, err := BuildWith([]byte("x"), "  ", "wzag_key", "name", ModelConfig{}); err == nil {
		t.Fatal("empty server URL must be rejected")
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName("本机采集端"); got != "本机采集端" {
		t.Fatalf("unexpected safe name: %s", got)
	}
	if got := safeName("../../etc/passwd"); strings.ContainsAny(got, "/\\") {
		t.Fatalf("path characters must be removed: %s", got)
	}
	if got := safeName("   "); got != "agent" {
		t.Fatalf("blank name fallback: %s", got)
	}
}
