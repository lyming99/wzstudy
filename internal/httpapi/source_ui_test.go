package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestSourceDialogUsesAgentIntegration(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	asset := request(t, handler, http.MethodGet, "/workspace/app.js", "", nil)
	if asset.Code != http.StatusOK {
		t.Fatal("app.js", asset.Code)
	}
	script := asset.Body.String()
	for _, expected := range []string{
		">Agent 录入</option>",
		"['agent','collector','skill','mcp']",
		"old?.integration||'agent'",
		"agent:'Agent 录入'",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("source dialog is missing %s", expected)
		}
	}
	if strings.Contains(script, ">API / Skill / MCP 录入</option>") {
		t.Fatal("legacy source type label is still rendered")
	}
}
