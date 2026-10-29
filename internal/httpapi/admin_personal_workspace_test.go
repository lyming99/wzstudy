package httpapi

import (
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestAdministratorUsesOwnWorkspaceAndManagementWithOneSession(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	learner, learnerToken := managedLearner(t, h, "admin-workspace-other")
	var login struct {
		Role          string              `json:"role"`
		User          study.User          `json:"user"`
		Administrator study.Administrator `json:"administrator"`
	}
	w := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	data(t, w, &login)
	if login.Role != "admin" || login.User.ID == "" || login.User.ID != login.Administrator.ID {
		t.Fatal("admin login did not include its own personal principal", w.Body.String())
	}
	cookie := roleCookie(t, w, userSessionCookie, false)
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/workspace/workspace", "/api/v1/user/auth/session", "/api/v1/client/learning"} {
		if got := cookieRequest(t, h, "GET", path, "", cookie, "", nil); got.Code != http.StatusOK {
			t.Fatalf("admin session denied %s: %d %s", path, got.Code, got.Body.String())
		}
	}
	var adminItem study.Item
	data(t, cookieRequest(t, h, "POST", "/api/v1/workspace/items", "http://example.com", cookie, "", study.ManagedInput{Status: "draft", Content: study.Content{Kind: "article", Title: "admin private", Content: "admin personal", Format: "text"}}), &adminItem)
	if adminItem.OwnerID != login.User.ID {
		t.Fatal("administrator data owner is not its personal ID", adminItem.OwnerID)
	}
	var learnerItem study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items", learnerToken, study.ManagedInput{Status: "draft", Content: study.Content{Kind: "article", Title: "learner private", Content: "learner personal", Format: "text"}}), &learnerItem)
	if learnerItem.OwnerID != learner.ID || learner.ID == login.User.ID {
		t.Fatal("workspace identities overlapped")
	}
	if got := request(t, h, "GET", "/api/v1/workspace/items/"+adminItem.ID, learnerToken, nil); got.Code != http.StatusNotFound {
		t.Fatal("learner accessed administrator data", got.Code)
	}
	if got := cookieRequest(t, h, "GET", "/api/v1/workspace/items/"+learnerItem.ID, "", cookie, "", nil); got.Code != http.StatusNotFound {
		t.Fatal("administrator accessed another user's data", got.Code)
	}
	var source struct {
		Source study.Source `json:"source"`
		Key    string       `json:"access_key"`
	}
	data(t, cookieRequest(t, h, "POST", "/api/v1/workspace/sources", "http://example.com", cookie, "", map[string]any{"slug": "admin-source", "name": "admin source", "permissions": []string{"ingest", "upload"}}), &source)
	if source.Source.OwnerID != login.User.ID {
		t.Fatal("admin source owner incorrect")
	}
	if got := request(t, h, "GET", "/api/v1/ingest/"+source.Source.ID+"/status", source.Key, nil); got.Code != http.StatusOK {
		t.Fatal("admin-owned source cannot authenticate", got.Code, got.Body.String())
	}
	var client struct {
		Client study.Client `json:"client"`
		Key    string       `json:"access_key"`
	}
	data(t, cookieRequest(t, h, "POST", "/api/v1/workspace/clients", "http://example.com", cookie, "", map[string]any{"slug": "admin-client", "name": "admin client", "permissions": []string{"read", "sync"}}), &client)
	if client.Client.OwnerID != login.User.ID {
		t.Fatal("admin client owner incorrect")
	}
	if got := request(t, h, "GET", "/api/v1/items", client.Key, nil); got.Code != http.StatusOK {
		t.Fatal("admin-owned client cannot authenticate", got.Code)
	}
	if got := request(t, h, "GET", "/api/v1/workspace/workspace", adminKey, nil); got.Code != http.StatusUnauthorized {
		t.Fatal("admin API key became a personal session", got.Code)
	}
	if got := cookieRequest(t, h, "GET", "/api/v1/admin/users", "", cookie, "invalid-explicit-token", nil); got.Code != http.StatusUnauthorized {
		t.Fatal("invalid explicit authorization fell back to admin cookie", got.Code)
	}
	if got := cookieRequest(t, h, "POST", "/api/v1/admin/users", "https://study.example", cookie, "", map[string]any{}); got.Code != http.StatusForbidden {
		t.Fatal("cross-origin cookie write reached management", got.Code)
	}
}

func TestAdministratorNativeTokenPasswordChangeAndRevocation(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	_, otherDevice := managedLearner(t, h, "admin-password-other")
	var native struct {
		Role  string     `json:"role"`
		Token string     `json:"access_token"`
		User  study.User `json:"user"`
	}
	w := request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": "admin@example.com", "password": "administrator-test-password", "session_mode": "token"})
	data(t, w, &native)
	if native.Role != "admin" || native.Token == "" || len(w.Result().Cookies()) != 0 {
		t.Fatal("admin native token login failed", w.Code)
	}
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/workspace/workspace", "/api/v1/user/auth/session"} {
		if got := request(t, h, "GET", path, native.Token, nil); got.Code != http.StatusOK {
			t.Fatal("admin native token denied", path, got.Code)
		}
	}
	if got := request(t, h, "POST", "/api/v1/user/auth/password", native.Token, map[string]string{"current_password": "wrong-password", "password": "new-administrator-password"}); got.Code != http.StatusUnauthorized {
		t.Fatal("invalid current password accepted", got.Code)
	}
	if got := request(t, h, "POST", "/api/v1/user/auth/password", native.Token, map[string]string{"current_password": "administrator-test-password", "password": "new-administrator-password"}); got.Code != http.StatusOK {
		t.Fatal("admin password change failed", got.Code, got.Body.String())
	}
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/workspace/workspace", "/api/v1/user/auth/session"} {
		if got := request(t, h, "GET", path, native.Token, nil); got.Code != http.StatusUnauthorized {
			t.Fatal("old administrator session survived password change", path, got.Code)
		}
	}
	if got := request(t, h, "GET", "/api/v1/user/auth/session", otherDevice, nil); got.Code != http.StatusOK {
		t.Fatal("admin password change revoked another user", got.Code)
	}
	if got := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"}); got.Code != http.StatusUnauthorized {
		t.Fatal("old password still worked", got.Code)
	}
	if got := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "new-administrator-password"}); got.Code != http.StatusOK {
		t.Fatal("new password failed", got.Code, got.Body.String())
	}
}
