package httpapi

import (
	"net/http"
	"regexp"
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
		"name=\"max_tool_calls\" type=\"number\" min=\"1\" max=\"1000\" value=\"200\"",
		"工具调用上限（单任务）",
		"aria-describedby=\"task-tool-call-hint\"",
		"工具调用上限仅限制单个 Agent 任务，各任务独立计数，不共享额度。",
		"max_tool_calls: numberValue(data, 'max_tool_calls', 200)",
		"max_tool_calls: template.budget?.max_tool_calls ?? 200",
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

func workspaceNavigationGroup(t *testing.T, body, label string) (string, string) {
	t.Helper()
	for _, match := range regexp.MustCompile(`(?s)<nav\b([^>]*)>(.*?)</nav>`).FindAllStringSubmatch(body, -1) {
		if strings.Contains(match[1], `aria-label="`+label+`"`) {
			return match[1], match[2]
		}
	}
	t.Fatalf("navigation group %q is missing", label)
	return "", ""
}

func TestWorkspaceAgentBoardsAreAvailableToEveryAccount(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	page := request(t, handler, http.MethodGet, "/workspace/", "", nil)
	body := page.Body.String()
	// Group membership, not position after the administrator group, controls access.
	attributes, tasks := workspaceNavigationGroup(t, body, "任务管理导航")
	if strings.Contains(attributes, "admin-only") {
		t.Fatal("task navigation must be available to every account")
	}
	for _, route := range []string{`data-route="tasks"`, `data-route="agent-access"`} {
		if !strings.Contains(tasks, route) {
			t.Fatalf("%s must belong to the shared task navigation group", route)
		}
	}
	attributes, users := workspaceNavigationGroup(t, body, "系统管理导航")
	if !strings.Contains(attributes, "admin-only") || !strings.Contains(users, `data-route="users"`) {
		t.Fatal("user management must stay administrator-only")
	}
}

func TestWorkspaceNavigationGroupsAndOrder(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	body := request(t, handler, http.MethodGet, "/workspace/", "", nil).Body.String()
	groups := []struct {
		label  string
		routes []string
		labels []string
	}{
		{"数据管理导航", []string{"library", "rss", "query", "catalog", "roadmaps"}, []string{"数据录入", "RSS 订阅", "数据列表", "合集管理", "学习路线图"}},
		{"任务管理导航", []string{"tasks", "agent-access"}, []string{"收集任务", "Agent 接入"}},
		{"客户端管理导航", []string{"clients", "learning"}, []string{"客户端管理", "学习数据"}},
		{"系统管理导航", []string{"users"}, []string{"用户管理"}},
	}
	lastGroup := -1
	for _, group := range groups {
		position := strings.Index(body, `aria-label="`+group.label+`"`)
		if position <= lastGroup {
			t.Fatalf("navigation group %q is out of order", group.label)
		}
		lastGroup = position
		_, content := workspaceNavigationGroup(t, body, group.label)
		if count := strings.Count(content, "data-route="); count != len(group.routes) {
			t.Fatalf("navigation group %q has %d entries, want %d", group.label, count, len(group.routes))
		}
		lastRoute := -1
		for i, route := range group.routes {
			marker := `data-route="` + route + `"`
			position = strings.Index(content, marker)
			if position <= lastRoute || strings.Count(body, marker) != 1 {
				t.Fatalf("navigation entry %q is missing, duplicated or out of order", route)
			}
			lastRoute = position
			if !strings.Contains(content, `<span class="nav-label">`+group.labels[i]+`</span>`) {
				t.Fatalf("navigation entry %q has an incorrect label", route)
			}
		}
	}
}
