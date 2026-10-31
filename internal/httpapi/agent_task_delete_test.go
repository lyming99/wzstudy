package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestAdminDeleteCollectionTask(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	created := request(t, handler, http.MethodPost, "/api/v1/admin/agent/tasks", adminKey, map[string]any{
		"goal": "删除接口测试任务",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", created.Code, created.Body.String())
	}
	var payload struct {
		Data study.CollectionTask `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	taskID := payload.Data.ID
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/tasks/"+taskID+"/delete", adminKey, map[string]any{}); w.Code != http.StatusConflict {
		t.Fatalf("running task must not be deleted: %d", w.Code)
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/tasks/"+taskID+"/cancel", adminKey, map[string]any{"reason": "测试"}); w.Code != http.StatusOK {
		t.Fatalf("cancel task: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/tasks/"+taskID+"/delete", adminKey, map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("delete task: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/tasks/"+taskID, adminKey, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted task must be gone: %d", w.Code)
	}
}
