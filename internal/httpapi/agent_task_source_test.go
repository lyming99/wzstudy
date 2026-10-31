package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestAdminTaskWithoutSourceUsesAutomaticSource(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	response := request(t, handler, http.MethodPost, "/api/v1/admin/agent/tasks", adminKey, map[string]any{
		"goal": "收集 Go 官方博客的最新文章",
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("dispatch without source: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data study.CollectionTask `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.SourceID == "" {
		t.Fatal("task must reference the automatic source")
	}
}
