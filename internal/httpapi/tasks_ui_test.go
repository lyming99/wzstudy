package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceTaskBoardAssetsAreEmbedded(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	page := request(t, handler, http.MethodGet, "/workspace/", "", nil)
	if page.Code != http.StatusOK {
		t.Fatal("workspace page", page.Code)
	}
	body := page.Body.String()
	for _, expected := range []string{
		`data-route="tasks"`,
		`/workspace/tasks.css`,
		`/workspace/tasks.js`,
		`采集任务`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("workspace page missing %s", expected)
		}
	}
	app := request(t, handler, http.MethodGet, "/workspace/app.js", "", nil)
	if app.Code != http.StatusOK || !strings.Contains(app.Body.String(), "tasks:tasksView.render") {
		t.Fatal("main app is missing the task board route registration")
	}
	if strings.Index(body, "/workspace/tasks.js") > strings.Index(body, "/workspace/app.js") {
		t.Fatal("task board bundle must load before the main app")
	}
	for _, path := range []string{"/workspace/tasks.js", "/workspace/tasks.css"} {
		asset := request(t, handler, http.MethodGet, path, "", nil)
		if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
			t.Fatalf("missing task board asset %s: %d", path, asset.Code)
		}
	}
	script := request(t, handler, http.MethodGet, "/workspace/tasks.js", "", nil).Body.String()
	for _, expected := range []string{
		"/admin/agent/tasks",
		"/admin/agent/status",
		"/workspace/sources",
		"dispatch-submit",
		"data-task-action=\"cancel\"",
		"data-task-action=\"retry\"",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("task board script missing %s", expected)
		}
	}
}

func TestWorkspaceTaskBoardIsAdministratorOnly(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	page := request(t, handler, http.MethodGet, "/workspace/", "", nil)
	body := page.Body.String()
	index := strings.Index(body, `data-route="tasks"`)
	if index < 0 {
		t.Fatal("task route is missing")
	}
	navStart := strings.LastIndex(body[:index], `class="nav-list admin-only"`)
	if navStart < 0 || strings.LastIndex(body[:index], `class="nav-list user-only"`) > navStart {
		t.Fatal("task board must live in the administrator-only navigation group")
	}
}
