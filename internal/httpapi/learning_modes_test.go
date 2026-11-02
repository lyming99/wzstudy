package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"wzstudy/internal/study"
)

func TestClientKeyLearningModesAndWorkspaceIsolation(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	item, err := service.SaveManaged(testWorkspaceContext(), "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "资讯", Content: "正文", Category: "科技", URL: "https://www.bilibili.com/video/test"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Publication(testWorkspaceContext(), item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	client, key := managedClient(t, h, "default-learning-client", nil)
	_, reader := managedClient(t, h, "explicit-reader", []string{"read"})
	_, ownerKey := workspaceLearner(t, h, "owner-learning")
	_, otherKey := managedLearner(t, h, "other-workspace-learning")
	var caps struct {
		Version  int    `json:"version"`
		Identity string `json:"identity"`
		ClientID string `json:"client_id"`
		Write    bool   `json:"write"`
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning/capabilities", key, nil), &caps)
	if caps.Version != 1 || caps.Identity != "client" || caps.ClientID != client.ID || !caps.Write {
		t.Fatal(caps)
	}
	payload := map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "view"}, map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "review_plan", "value": true, "review_intervals_minutes": []int{5, 60}}}}
	var ack study.LearningResult
	data(t, request(t, h, "POST", "/api/v1/client/learning/events", key, payload), &ack)
	if ack.Accepted != 2 || len(ack.States) != 1 || ack.States[0].ClientID != client.ID || !ack.States[0].ReviewActive {
		t.Fatal(ack)
	}
	data(t, request(t, h, "POST", "/api/v1/client/learning/events", key, payload), &ack)
	if ack.Duplicates != 2 {
		t.Fatal(ack)
	}
	if w := request(t, h, "POST", "/api/v1/client/learning/events", reader, payload); w.Code != http.StatusUnauthorized {
		t.Fatal("read-only client wrote learning", w.Code)
	}
	var categories study.LearningModeChoices
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/categories?mode=news", reader, nil), &categories)
	if categories.Total != 1 || categories.Items[0].Title != "资讯内容" || categories.Items[0].Count != 1 {
		t.Fatal(categories)
	}
	var page study.LearningModePage
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=review", key, nil), &page)
	if page.Total != 1 {
		t.Fatal(page)
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=review", ownerKey, nil), &page)
	if page.Total != 0 {
		t.Fatal("client plans inherited by user", page)
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news", otherKey, nil), &page)
	if page.Total != 0 {
		t.Fatal("other workspace leaked", page)
	}
	for _, path := range []string{"/api/v1/client/learning/modes/items?mode=random&client_id=" + client.ID, "/api/v1/client/learning/modes/items?mode=random&seed=-1", "/api/v1/client/learning/modes/items?mode=route", "/api/v1/client/learning/modes/categories?mode=news&mode=review"} {
		if w := request(t, h, "GET", path, key, nil); w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := request(t, h, "GET", "/api/v1/workspace/workspace", key, nil); w.Code != 401 {
		t.Fatal("client gained management", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/client/learning/modes/items?mode=news", adminKey, nil); w.Code != 401 {
		t.Fatal("global admin key gained data", w.Code)
	}
}

func TestLearningCookieWritesRetainCSRFProtection(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	user, _ := managedLearner(t, h, "mode-cookie")
	login := roleRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"email": user.Email, "password": learnerPassword})
	cookie := roleCookie(t, login, userSessionCookie, false)
	for _, origin := range []string{"", "https://evil.example"} {
		for _, path := range []string{"/api/v1/client/learning/events", "/api/v1/client/feedback", "/api/v1/client/learning/drafts/commands", "/api/v1/client/learning/collections/commands"} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{"events":[]}`))
			r.Header.Set("Content-Type", "application/json")
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || (origin == "" && !strings.Contains(w.Body.String(), "csrf_denied")) {
				t.Fatal(path, origin, w.Code, w.Body.String())
			}
		}
	}
}
