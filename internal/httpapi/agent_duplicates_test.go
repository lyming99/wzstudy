package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"wzstudy/internal/study"
)

func TestAgentDuplicatePreflightIsScopedAndBooleanOnly(t *testing.T) {
	service, handler, _, agent, key, task := agentApp(t)
	w := request(t, handler, "POST", "/api/v1/agent/tasks/claim", key, map[string]any{})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	_, err := service.IngestAgentTaskItems(testWorkspaceContext(), agent.ID, task.ID, 1, []study.Content{{ExternalID: "BV1oBxT6SECq", Title: "private stored title", Content: "private stored body", URL: "https://www.bilibili.com/video/BV1oBxT6SECq"}})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"attempt": 1, "items": []map[string]string{{"external_id": "alternate-id", "url": "https://www.bilibili.com/video/BV1oBxT6SECq/?from=search"}, {"external_id": "unknown"}}}
	w = request(t, handler, "POST", "/api/v1/agent/tasks/"+task.ID+"/duplicates", key, body)
	if w.Code != 200 {
		t.Fatalf("preflight %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private stored") {
		t.Fatal("library contents leaked")
	}
	var result struct {
		Data struct {
			Items []study.CollectionDuplicate `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Items) != 2 || !result.Data.Items[0].Exists || result.Data.Items[1].Exists {
		t.Fatal(w.Body.String())
	}
	if w := request(t, handler, http.MethodPost, "/api/v1/agent/tasks/"+task.ID+"/duplicates", "", body); w.Code != 401 {
		t.Fatal("anonymous request accepted")
	}
	body["attempt"] = 2
	if w := request(t, handler, http.MethodPost, "/api/v1/agent/tasks/"+task.ID+"/duplicates", key, body); w.Code != 409 {
		t.Fatal("stale attempt accepted", w.Code)
	}
}
