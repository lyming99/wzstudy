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
		`data-route="rss"`,
		`data-route="agent-access"`,
		`/workspace/tasks.css`,
		`/workspace/tasks.js`,
		`/workspace/agent-access.js`,
		`收集任务`,
		`Agent 接入`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("workspace page missing %s", expected)
		}
	}
	app := request(t, handler, http.MethodGet, "/workspace/app.js", "", nil)
	appScript := app.Body.String()
	for _, expected := range []string{"tasks:tasksView.render", "rss:renderRssSources", "'agent-access':renderAgentAccess", "agentAccessView.section", "agentAccessView.load", "data-agent-action=\"download-binary\"", "/admin/agent/deployments", "download-agent", "deploy-submit", "deploy-provider", "deploy-base-url-field", "deploy-models", "deploy-model-options", "deploy-model-toggle", "data-deploy-model", "renderDeployModels", "resetDeployModels", "loadDeployModels", "runtime-workdir", "runtime-installdir", "runtime-provider", "runtime-apikey", "runtime-models", "runtime-model-menu", "data-runtime-model", "loadAgentRuntime", "/admin/agent/runtime", "default_work_dir", "default_install_dir", "route==='users'", "data-query-select", "data-query-select-all", "query-batch-delete", "/workspace/items/batch-delete"} {
		if !strings.Contains(appScript, expected) {
			t.Fatalf("main app is missing %s", expected)
		}
	}
	if strings.Contains(appScript, "deploy-name") {
		t.Fatal("deployment dialog must not ask for a name")
	}
	if strings.Contains(appScript, `list="deploy-model-options"`) || strings.Contains(appScript, `<datalist id="deploy-model-options"`) {
		t.Fatal("deployment model picker must show all options rather than use filtered native datalist")
	}
	if strings.Index(body, "/workspace/tasks.js") > strings.Index(body, "/workspace/app.js") {
		t.Fatal("task board bundle must load before the main app")
	}
	if strings.Index(body, "/workspace/agent-access.js") > strings.Index(body, "/workspace/app.js") {
		t.Fatal("agent access bundle must load before the main app")
	}
	for _, path := range []string{"/workspace/tasks.js", "/workspace/tasks.css", "/workspace/agent-access.js"} {
		asset := request(t, handler, http.MethodGet, path, "", nil)
		if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
			t.Fatalf("missing task board asset %s: %d", path, asset.Code)
		}
	}
	script := request(t, handler, http.MethodGet, "/workspace/tasks.js", "", nil).Body.String()
	for _, expected := range []string{
		"const ADMIN_BASE = '/admin/agent'",
		"const WORKSPACE_BASE = '/workspace/agent'",
		"agentBase()",
		"/workspace/sources",
		"dispatch-submit",
		"data-task-action=\"cancel\"",
		"data-task-action=\"retry\"",
		"release-upload",
		"release-row",
		"自动创建（Agent 收集）",
		"data-task-action=\"delete\"",
		"name=\"content_type\"",
		"name=\"max_rounds\" type=\"number\" min=\"1\" max=\"1000\" value=\"100\"",
		"name=\"max_tool_calls\" type=\"number\" min=\"1\" max=\"1000\" value=\"100\"",
		"task-templates",
		"data-task-action=\"template-apply\"",
		"data-task-action=\"template-delete\"",
		"name=\"template_name\"",
		"保存为模板",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("task board script missing %s", expected)
		}
	}
	if strings.Contains(script, "先准备采集来源") {
		t.Fatal("dispatch dialog must not require a pre-created source")
	}
	agentScript := request(t, handler, http.MethodGet, "/workspace/agent-access.js", "", nil).Body.String()
	for _, expected := range []string{
		"const ADMIN_BASE = '/admin/agent'",
		"const WORKSPACE_BASE = '/workspace/agent'",
		"download-binary",
		"package-download",
		"package-provider",
		"package-model",
		"package-api-key",
		"view-key",
		"download-zip",
		"agent-key-value",
		"查看密钥",
		"下载agent程序",
		"密钥已打码",
		"复制完整 agent.env",
		"wzag_********",
		"agent.env",
		"轮换密钥",
		"新建接入",
		"data-agent-action=\"delete\"",
		"接入身份已删除",
	} {
		if !strings.Contains(agentScript, expected) {
			t.Fatalf("agent access script missing %s", expected)
		}
	}
}

func TestWorkspaceAgentBoardsAreAvailableToEveryAccount(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	page := request(t, handler, http.MethodGet, "/workspace/", "", nil)
	body := page.Body.String()
	adminGroup := strings.Index(body, `class="nav-group-title admin-only"`)
	if adminGroup < 0 {
		t.Fatal("administrator navigation group is missing")
	}
	for _, route := range []string{`data-route="agent-access"`, `data-route="tasks"`} {
		index := strings.Index(body, route)
		if index < 0 {
			t.Fatalf("%s is missing", route)
		}
		if index > adminGroup {
			t.Fatalf("%s must be available outside the administrator-only navigation group", route)
		}
	}
	users := strings.Index(body, `data-route="users"`)
	if users < adminGroup {
		t.Fatal("user management must stay administrator-only")
	}
}
