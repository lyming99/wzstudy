package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

// A normal account may bring its own agent, but every identity, task, event and
// template stays inside that account's workspace. The server-hosted built-in
// agent remains an administrator-only capability.
func TestWorkspaceAgentDeletionIsTenantScopedAndBlocksActiveTasks(t *testing.T) {
	service, _, dir := managementApp(t, 1<<20)
	handler := New(config.Config{
		Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: legacyReadKey,
		DataDir: dir, MaxUploadBytes: 1 << 20, AgentEnabled: true, AgentLeaseSeconds: 300,
	}, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, aliceToken := managedLearner(t, handler, "workspace-agent-delete-alice")
	_, bobToken := managedLearner(t, handler, "workspace-agent-delete-bob")

	var created struct {
		Agent     study.Agent `json:"agent"`
		AccessKey string      `json:"access_key"`
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/agents", aliceToken, map[string]any{
		"slug": "alice-delete-agent", "name": "Alice Delete Agent",
	}), &created)
	path := "/api/v1/workspace/agent/agents/" + created.Agent.ID

	if w := request(t, handler, http.MethodPost, path+"/delete", bobToken, map[string]any{"revision": created.Agent.Revision}); w.Code != http.StatusNotFound {
		t.Fatalf("bob deleted alice agent: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, path+"/delete", aliceToken, map[string]any{"revision": created.Agent.Revision + 1}); w.Code != http.StatusConflict {
		t.Fatalf("stale revision deleted agent: %d %s", w.Code, w.Body.String())
	}

	var task study.CollectionTask
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/tasks", aliceToken, map[string]any{
		"goal":              "采集用于删除校验的示例文章",
		"assigned_agent_id": created.Agent.ID,
	}), &task)
	blocked := request(t, handler, http.MethodPost, path+"/delete", aliceToken, map[string]any{"revision": created.Agent.Revision})
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "agent_in_use") {
		t.Fatalf("active task did not block deletion: %d %s", blocked.Code, blocked.Body.String())
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/tasks/"+task.ID+"/cancel", aliceToken, map[string]any{
		"reason": "清理测试任务",
	}), &task)

	deleted := request(t, handler, http.MethodPost, path+"/delete", aliceToken, map[string]any{"revision": created.Agent.Revision})
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete agent: %d %s", deleted.Code, deleted.Body.String())
	}
	var agents []study.Agent
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents", aliceToken, nil), &agents)
	if len(agents) != 0 {
		t.Fatalf("deleted agent remains listed: %+v", agents)
	}
	if w := request(t, handler, http.MethodGet, path, aliceToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted agent remains readable: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/agent/tasks/claim", created.AccessKey, map[string]any{"capacity": 1}); w.Code != http.StatusUnauthorized {
		t.Fatalf("deleted agent key still authenticates: %d %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceAgentAccessIsTenantScopedAndBuiltinsStayAdminOnly(t *testing.T) {
	service, _, dir := managementApp(t, 1<<20)
	handler := New(config.Config{
		Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: legacyReadKey,
		DataDir: dir, MaxUploadBytes: 1 << 20, AgentEnabled: true, AgentLeaseSeconds: 300,
	}, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	alice, aliceToken := managedLearner(t, handler, "workspace-agent-alice")
	_, bobToken := managedLearner(t, handler, "workspace-agent-bob")

	var created struct {
		Agent     study.Agent `json:"agent"`
		AccessKey string      `json:"access_key"`
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/agents", aliceToken, map[string]any{
		"slug": "alice-agent", "name": "Alice Agent",
	}), &created)
	if created.Agent.OwnerID != alice.ID {
		t.Fatalf("agent owner = %q, want %q", created.Agent.OwnerID, alice.ID)
	}
	if !strings.HasPrefix(created.AccessKey, "wzag_") {
		t.Fatalf("missing one-time agent key: %q", created.AccessKey)
	}

	var viewed struct {
		AccessKey string `json:"access_key"`
	}
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/key", aliceToken, nil), &viewed)
	if viewed.AccessKey != created.AccessKey {
		t.Fatalf("viewed key = %q, want %q", viewed.AccessKey, created.AccessKey)
	}
	var renamed study.Agent
	data(t, request(t, handler, http.MethodPut, "/api/v1/workspace/agent/agents/"+created.Agent.ID, aliceToken, map[string]any{
		"revision": created.Agent.Revision, "slug": created.Agent.Slug, "name": "Alice Agent Updated", "enabled": true,
	}), &renamed)
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/key", aliceToken, nil), &viewed)
	if viewed.AccessKey != created.AccessKey {
		t.Fatalf("updating metadata lost the readable key: %q", viewed.AccessKey)
	}
	var rotated struct {
		Agent     study.Agent `json:"agent"`
		AccessKey string      `json:"access_key"`
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/key", aliceToken, map[string]any{"revision": renamed.Revision}), &rotated)
	if rotated.AccessKey == "" || rotated.AccessKey == created.AccessKey {
		t.Fatalf("rotate did not return a new key: %q", rotated.AccessKey)
	}
	created.Agent, created.AccessKey = rotated.Agent, rotated.AccessKey
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/key", aliceToken, nil), &viewed)
	if viewed.AccessKey != created.AccessKey {
		t.Fatalf("viewed key after rotate = %q, want %q", viewed.AccessKey, created.AccessKey)
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/key", bobToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("bob viewed alice key: %d %s", w.Code, w.Body.String())
	}

	packageResponse := request(t, handler, http.MethodPost, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/package", aliceToken, map[string]any{
		"access_key": created.AccessKey,
		"model": map[string]any{
			"provider": "deepseek", "model": "deepseek-chat", "api_key": "sk-deployment-test",
		},
	})
	if packageResponse.Code != http.StatusOK {
		t.Fatalf("package agent: %d %s", packageResponse.Code, packageResponse.Body.String())
	}
	if contentType := packageResponse.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("package content type = %q", contentType)
	}
	archive, err := zip.NewReader(bytes.NewReader(packageResponse.Body.Bytes()), int64(packageResponse.Body.Len()))
	if err != nil {
		t.Fatalf("read package zip: %v", err)
	}
	names := map[string]bool{}
	for _, entry := range archive.File {
		names[entry.Name] = true
	}
	if !names["wzstudy-agent.exe"] || !names["agent.env"] || !names["start-agent.cmd"] {
		t.Fatalf("package missing runtime files: %+v", names)
	}
	envEntry, err := archive.Open("agent.env")
	if err != nil {
		t.Fatalf("open packaged agent.env: %v", err)
	}
	envBytes, err := io.ReadAll(envEntry)
	_ = envEntry.Close()
	if err != nil {
		t.Fatalf("read packaged agent.env: %v", err)
	}
	env := string(envBytes)
	for _, expected := range []string{
		"WZSTUDY_AGENT_KEY=" + created.AccessKey,
		"AGENT_MODEL_BASE_URL=https://api.deepseek.com/v1",
		"AGENT_MODEL=deepseek-chat",
		"AGENT_MODEL_API_KEY=sk-deployment-test",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("packaged agent.env missing %q: %s", expected, env)
		}
	}
	var packageProfile struct {
		Package study.AgentPackageProfile `json:"package"`
	}
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/key", aliceToken, nil), &packageProfile)
	if packageProfile.Package.Provider != "deepseek" || packageProfile.Package.Model != "deepseek-chat" ||
		packageProfile.Package.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("package profile was not remembered: %+v", packageProfile.Package)
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/package", aliceToken, map[string]any{"access_key": "wzag_wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("package with wrong key: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/workspace/agent/agents/"+created.Agent.ID+"/package", bobToken, map[string]any{"access_key": created.AccessKey}); w.Code != http.StatusNotFound {
		t.Fatalf("bob packaged alice agent: %d %s", w.Code, w.Body.String())
	}

	var aliceAgents []study.Agent
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents", aliceToken, nil), &aliceAgents)
	if len(aliceAgents) != 1 || aliceAgents[0].ID != created.Agent.ID {
		t.Fatalf("alice agents = %+v", aliceAgents)
	}
	var bobAgents []study.Agent
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents", bobToken, nil), &bobAgents)
	if len(bobAgents) != 0 {
		t.Fatalf("bob saw another workspace's agent: %+v", bobAgents)
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/workspace/agent/agents/"+created.Agent.ID, bobToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("bob read alice agent: %d %s", w.Code, w.Body.String())
	}

	var task study.CollectionTask
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/tasks", aliceToken, map[string]any{
		"goal":              "采集 Alice 的示例文章",
		"assigned_agent_id": created.Agent.ID,
		"constraints":       map[string]any{"max_items": 2},
	}), &task)
	if task.OwnerID != alice.ID || task.AssignedAgentID != created.Agent.ID {
		t.Fatalf("unexpected task ownership: %+v", task)
	}

	var template study.AgentTaskTemplate
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/task-templates", aliceToken, map[string]any{
		"name": "Alice 模板", "goal": "采集 Alice 的示例文章",
	}), &template)
	if template.OwnerID != alice.ID {
		t.Fatalf("unexpected template ownership: %+v", template)
	}
	var bobTemplates []study.AgentTaskTemplate
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/task-templates", bobToken, nil), &bobTemplates)
	if len(bobTemplates) != 0 {
		t.Fatalf("bob saw another workspace's templates: %+v", bobTemplates)
	}

	var bobTask study.CollectionTask
	data(t, request(t, handler, http.MethodPost, "/api/v1/workspace/agent/tasks", bobToken, map[string]any{
		"goal": "采集 Bob 的示例文章",
	}), &bobTask)

	var bobTasks struct {
		Tasks []study.CollectionTask `json:"tasks"`
		Total int                    `json:"total"`
	}
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/tasks", bobToken, nil), &bobTasks)
	if len(bobTasks.Tasks) != 1 || bobTasks.Total != 1 || bobTasks.Tasks[0].ID != bobTask.ID {
		t.Fatalf("bob saw the wrong workspace tasks: %+v", bobTasks)
	}
	for _, path := range []string{
		"/api/v1/workspace/agent/tasks/" + task.ID,
		"/api/v1/workspace/agent/tasks/" + task.ID + "/events",
	} {
		if w := request(t, handler, http.MethodGet, path, bobToken, nil); w.Code != http.StatusNotFound {
			t.Fatalf("bob read alice task resource %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/workspace/agent/tasks/"+task.ID+"/cancel", bobToken, map[string]any{"reason": "越权"}); w.Code != http.StatusNotFound {
		t.Fatalf("bob cancelled alice task: %d %s", w.Code, w.Body.String())
	}

	var claimed struct {
		Task study.CollectionTask `json:"task"`
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/agent/tasks/claim", created.AccessKey, map[string]any{"capacity": 1}), &claimed)
	if claimed.Task.ID != task.ID {
		t.Fatalf("agent claimed a foreign task: %+v", claimed.Task)
	}
	var bobAfterClaim study.CollectionTask
	data(t, request(t, handler, http.MethodGet, "/api/v1/workspace/agent/tasks/"+bobTask.ID, bobToken, nil), &bobAfterClaim)
	if bobAfterClaim.Status != study.TaskStatusPending {
		t.Fatalf("alice agent changed bob task: %+v", bobAfterClaim)
	}

	var adminAgents []study.Agent
	data(t, request(t, handler, http.MethodGet, "/api/v1/admin/agent/agents", adminKey, nil), &adminAgents)
	for _, agent := range adminAgents {
		if agent.ID == created.Agent.ID {
			t.Fatal("administrator console exposed a normal user's agent")
		}
	}
	var adminTasks struct {
		Tasks []study.CollectionTask `json:"tasks"`
		Total int                    `json:"total"`
	}
	data(t, request(t, handler, http.MethodGet, "/api/v1/admin/agent/tasks", adminKey, nil), &adminTasks)
	if adminTasks.Total != 0 || len(adminTasks.Tasks) != 0 {
		t.Fatalf("administrator console exposed normal user tasks: %+v", adminTasks)
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/workspace/agent/tasks", created.AccessKey, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("agent key acted as a user session: %d %s", w.Code, w.Body.String())
	}

	for path, method := range map[string]string{
		"/api/v1/admin/agent/status":                              http.MethodGet,
		"/api/v1/admin/agent/agents":                              http.MethodGet,
		"/api/v1/admin/agent/agents/" + created.Agent.ID + "/key": http.MethodGet,
		"/api/v1/admin/agent/tasks":                               http.MethodGet,
		"/api/v1/admin/agent/runtime":                             http.MethodGet,
		"/api/v1/admin/agent/deployments":                         http.MethodPost,
		"/api/v1/admin/agent/models":                              http.MethodPost,
		"/api/v1/admin/agent/binary":                              http.MethodGet,
		"/api/v1/admin/agent/releases":                            http.MethodGet,
	} {
		if w := request(t, handler, method, path, aliceToken, map[string]any{}); w.Code != http.StatusUnauthorized {
			t.Fatalf("normal account reached administrator-only %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	var adminCreated struct {
		Agent     study.Agent `json:"agent"`
		AccessKey string      `json:"access_key"`
	}
	data(t, request(t, handler, http.MethodPost, "/api/v1/admin/agent/agents", adminKey, map[string]any{
		"slug": "admin-package-agent", "name": "Admin Package Agent",
	}), &adminCreated)
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/agents/"+adminCreated.Agent.ID+"/package", adminKey, map[string]any{
		"access_key": adminCreated.AccessKey,
		"model":      map[string]any{"provider": "openai", "model": "gpt-4o-mini", "api_key": "sk-admin-package"},
	}); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("administrator package endpoint: %d %s", w.Code, w.Body.String())
	}
	var adminViewed struct {
		AccessKey string `json:"access_key"`
	}
	data(t, request(t, handler, http.MethodGet, "/api/v1/admin/agent/agents/"+adminCreated.Agent.ID+"/key", adminKey, nil), &adminViewed)
	if adminViewed.AccessKey != adminCreated.AccessKey {
		t.Fatalf("administrator viewed key = %q, want %q", adminViewed.AccessKey, adminCreated.AccessKey)
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/runtime", adminKey, nil); w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("administrator lost built-in runtime: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/agents", adminKey, nil); w.Code != http.StatusOK {
		t.Fatalf("administrator lost agent console: %d %s", w.Code, w.Body.String())
	}
}
