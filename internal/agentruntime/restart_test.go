package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wzstudy/internal/study"
)

func TestMain(m *testing.M) {
	if os.Getenv("WZSTUDY_RUNTIME_TEST_HELPER") == "1" && len(os.Args) == 2 && os.Args[1] == "run" {
		raw, _ := json.Marshal(map[string]string{"model": os.Getenv("AGENT_MODEL"), "base_url": os.Getenv("AGENT_MODEL_BASE_URL"), "key": os.Getenv("AGENT_MODEL_API_KEY")})
		if err := os.WriteFile("applied-"+strconv.Itoa(os.Getpid())+".json", raw, 0600); err != nil {
			os.Exit(2)
		}
		// Mimic the long-running agent without network access or model calls.
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(m.Run())
}

func executableRuntimeFixture(t *testing.T) string {
	t.Helper()
	dir := runtimeFixture(t)
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(dir, agentBinaryName()), os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := os.Chmod(filepath.Join(dir, agentBinaryName()), 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func appliedModel(t *testing.T, work string, pid int) map[string]string {
	t.Helper()
	file := filepath.Join(work, "applied-"+strconv.Itoa(pid)+".json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(file)
		if err == nil {
			var value map[string]string
			if json.Unmarshal(raw, &value) == nil {
				return value
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child process did not apply settings")
	return nil
}

func TestRestartReplacesProcessAndAppliesSavedSettings(t *testing.T) {
	t.Setenv("WZSTUDY_RUNTIME_TEST_HELPER", "1")
	manager := New(study.NewService(study.NewMemoryStore()), nil)
	ctx := context.Background()
	dir := executableRuntimeFixture(t)
	work := filepath.Join(dir, "work")
	if err := manager.UpdateSettings(ctx, dir, work, true, ModelSettings{Provider: "custom", BaseURL: "https://model.example/v1", Model: "before", APIKey: "test-key-1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(ctx) })
	// Restart also starts an initially stopped agent.
	if err := manager.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	oldPID := manager.Status().PID
	if appliedModel(t, work, oldPID)["model"] != "before" {
		t.Fatal("wrong initial settings")
	}
	if err := manager.UpdateSettings(ctx, dir, work, true, ModelSettings{Provider: "custom", BaseURL: "https://new-model.example/v1", Model: "after", APIKey: "test-key-2"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	current := manager.Status()
	if !current.Running || current.PID == oldPID || current.LastError != "" {
		t.Fatalf("restart state: %+v", current)
	}
	applied := appliedModel(t, work, current.PID)
	if applied["model"] != "after" || applied["base_url"] != "https://new-model.example/v1" || applied["key"] != "test-key-2" {
		t.Fatal("new process did not apply updated model settings")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := manager.Restart(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation", err)
	}
	if manager.Status().PID != current.PID {
		t.Fatal("canceled restart stopped healthy process")
	}
}

func TestRestartWithoutConfiguration(t *testing.T) {
	manager := New(study.NewService(study.NewMemoryStore()), nil)
	if err := manager.Restart(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
}
