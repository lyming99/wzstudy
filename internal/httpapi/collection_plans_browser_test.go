package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"wzstudy/internal/study"
)

// Opt-in browser contract uses only an isolated in-memory HTTP test server.
// Requires Python Playwright and its Chromium runtime, never a live workspace.
func TestCollectionPlansBrowser(t *testing.T) {
	if os.Getenv("WZ_COLLECTION_BROWSER_TEST") != "1" {
		t.Skip("set WZ_COLLECTION_BROWSER_TEST=1 to run isolated Playwright coverage")
	}
	s, handler := collectionApp(t)
	yes := true
	if _, err := s.CreateUser(context.Background(), study.UserInput{Email: "collection-browser@example.com", DisplayName: "Browser User", Password: "collection-browser-test-password", Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "test_collection_plans_browser.py"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python", script, server.URL)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser test failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}
