package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestAdminAgentTaskTemplates(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	path := "/api/v1/admin/agent/task-templates"

	empty := request(t, handler, http.MethodGet, path, adminKey, nil)
	if empty.Code != http.StatusOK {
		t.Fatalf("list templates: %d %s", empty.Code, empty.Body.String())
	}
	var listed struct {
		Data []study.AgentTaskTemplate `json:"data"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Data == nil || len(listed.Data) != 0 {
		t.Fatalf("new workspace must have no templates: %+v", listed.Data)
	}

	created := request(t, handler, http.MethodPost, path, adminKey, map[string]any{
		"name": "官方博客文章",
		"goal": "采集 Go 官方博客最近三篇文章",
		"constraints": map[string]any{
			"max_items": 3, "content_type": "article", "language": "zh-CN", "tags": []string{"Go"},
		},
		"budget":   map[string]any{"max_rounds": 10, "max_tool_calls": 12, "timeout_seconds": 120},
		"priority": 2,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create template: %d %s", created.Code, created.Body.String())
	}
	var payload struct {
		Data study.AgentTaskTemplate `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ID == "" || payload.Data.Constraints.MaxItems != 3 || payload.Data.Budget.MaxRounds != 10 || payload.Data.Priority != 2 {
		t.Fatalf("unexpected template: %+v", payload.Data)
	}

	if w := request(t, handler, http.MethodPost, path, adminKey, map[string]any{"name": "官方博客文章"}); w.Code != http.StatusConflict {
		t.Fatalf("duplicate name must conflict: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, path, adminKey, map[string]any{"name": ""}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty name must be rejected: %d", w.Code)
	}

	listed = struct {
		Data []study.AgentTaskTemplate `json:"data"`
	}{}
	response := request(t, handler, http.MethodGet, path, adminKey, nil)
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != payload.Data.ID {
		t.Fatalf("template was not listed: %+v", listed.Data)
	}

	if w := request(t, handler, http.MethodPost, path+"/"+payload.Data.ID+"/delete", adminKey, map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("delete template: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, path+"/"+payload.Data.ID+"/delete", adminKey, map[string]any{}); w.Code != http.StatusNotFound {
		t.Fatalf("delete twice must be missing: %d", w.Code)
	}
	if w := request(t, handler, http.MethodGet, path, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous template list must be unauthorized: %d", w.Code)
	}
}
