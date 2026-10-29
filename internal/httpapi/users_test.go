package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

const learnerPassword = "test-learner-password-only"

func managedLearner(t *testing.T, h http.Handler, name string) (study.User, string) {
	t.Helper()
	var user study.User
	data(t, request(t, h, "POST", "/api/v1/admin/users", adminKey, map[string]any{"email": name + "@example.test", "display_name": name, "password": learnerPassword}), &user)
	var session struct {
		Token string     `json:"access_token"`
		User  study.User `json:"user"`
	}
	data(t, request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": user.Email, "password": learnerPassword, "session_mode": "token"}), &session)
	if session.User.ID != user.ID || !strings.HasPrefix(session.Token, "wzus_") {
		t.Fatal("missing learner identity")
	}
	return user, session.Token
}

func cookieRequest(t *testing.T, h http.Handler, method, path, origin string, cookie *http.Cookie, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLearnerAccountManagementAndRoleBoundary(t *testing.T) {
	_, h := app()
	user, token := managedLearner(t, h, "alice")
	_, clientKey := managedClient(t, h, "shared-app", []string{"read", "sync", "feedback"})
	source, sourceKey := create(t, h, false)
	for _, key := range []string{"", token, clientKey, readKey, sourceKey} {
		for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/items", "/api/v1/admin/sources"} {
			if w := request(t, h, "POST", path, key, map[string]any{}); w.Code != 401 {
				t.Fatalf("non-admin wrote %s: %d", path, w.Code)
			}
		}
	}
	if w := request(t, h, "POST", "/api/v1/ingest/"+source.ID, token, map[string]any{"items": []any{}}); w.Code != 401 {
		t.Fatal("learner gained ingestion", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/admin/uploads", token, nil); w.Code != 401 {
		t.Fatal("learner gained upload", w.Code)
	}
	for _, key := range []string{"", clientKey, sourceKey, readKey, adminKey} {
		for _, path := range []string{"/api/v1/client/learning", "/api/v1/client/learning/capabilities", "/api/v1/client/feedback", "/api/v1/client/recommendations", "/api/v1/client/learning/collections", "/api/v1/user/auth/session"} {
			if w := request(t, h, "GET", path, key, nil); w.Code != 401 {
				t.Fatalf("non-user read %s: %d", path, w.Code)
			}
		}
		for _, path := range []string{"/api/v1/client/learning/events", "/api/v1/client/feedback", "/api/v1/client/feedback/import-legacy", "/api/v1/client/learning/drafts/commands", "/api/v1/client/learning/drafts/import-legacy", "/api/v1/client/learning/collections/commands", "/api/v1/client/learning/collections/import-legacy", "/api/v1/client/learning/import-legacy-records", "/api/v1/client/learning/import-legacy-reviews"} {
			if w := request(t, h, "POST", path, key, map[string]any{}); w.Code != 401 {
				t.Fatalf("non-user wrote %s: %d", path, w.Code)
			}
		}
	}
	for _, path := range []string{"/api/v1/items", "/api/v1/sources", "/api/v1/catalog/collections", "/api/v1/changes"} {
		if w := request(t, h, "GET", path, token, nil); w.Code != 200 {
			t.Fatal("learner cannot read public content", path, w.Code)
		}
	}
	if w := request(t, h, "GET", "/api/v1/items?include_feedback=true", clientKey, nil); w.Code != 401 {
		t.Fatal("application key queried personal feedback", w.Code)
	}
	for _, path := range []string{"/api/v1/client/learning?user_id=" + user.ID, "/api/v1/items?user_id=" + user.ID} {
		if w := request(t, h, "GET", path, token, nil); w.Code != 400 {
			t.Fatal("owner spoof", w.Code)
		}
	}
	w := request(t, h, "GET", "/api/v1/admin/users", adminKey, nil)
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "credential_version") || strings.Contains(w.Body.String(), token) {
		t.Fatal("account secrets exposed")
	}
	if w := request(t, h, "POST", "/api/v1/admin/users", adminKey, map[string]any{"email": "ALICE@example.test", "display_name": "duplicate", "password": learnerPassword}); w.Code != 409 {
		t.Fatal("duplicate email", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/admin/users", adminKey, map[string]any{"email": "role@example.test", "display_name": "role", "password": learnerPassword, "role": "admin"}); w.Code != 400 {
		t.Fatal("role injection", w.Code)
	}
	var updated study.User
	data(t, request(t, h, "PUT", "/api/v1/admin/users/"+user.ID, adminKey, map[string]any{"revision": user.Revision, "email": user.Email, "display_name": user.DisplayName, "enabled": false}), &updated)
	if w := request(t, h, "GET", "/api/v1/client/learning", token, nil); w.Code != 401 {
		t.Fatal("disabled user still authenticated", w.Code)
	}
	if w := request(t, h, "PUT", "/api/v1/admin/users/"+user.ID, adminKey, map[string]any{"revision": user.Revision, "email": user.Email, "display_name": user.DisplayName}); w.Code != 409 {
		t.Fatal("stale revision accepted", w.Code)
	}
}

func TestLearnerBrowserCookieAndRevocation(t *testing.T) {
	_, h := app()
	user, native := managedLearner(t, h, "browser")
	login := request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": user.Email, "password": learnerPassword})
	if login.Code != 200 || strings.Contains(login.Body.String(), "access_token") {
		t.Fatal("browser token leaked", login.Code)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != userSessionCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatal("unsafe browser cookie")
	}
	cookie := cookies[0]
	for _, path := range []string{"/api/v1/user/auth/session", "/api/v1/items", "/api/v1/client/learning"} {
		if w := cookieRequest(t, h, "GET", path, "", cookie, "", nil); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := cookieRequest(t, h, "GET", "/api/v1/admin/users", "", cookie, "", nil); w.Code != 401 {
		t.Fatal("user cookie became administrator")
	}
	if w := cookieRequest(t, h, "GET", "/api/v1/client/learning", "", cookie, adminKey, nil); w.Code != 401 {
		t.Fatal("explicit administrator key fell back to user cookie")
	}
	for _, origin := range []string{"", "https://study.example"} {
		if w := cookieRequest(t, h, "POST", "/api/v1/user/auth/logout", origin, cookie, "", nil); w.Code != 403 {
			t.Fatal("CSRF accepted", origin, w.Code)
		}
	}
	if w := cookieRequest(t, h, "POST", "/api/v1/user/auth/logout", "http://example.com", cookie, "", nil); w.Code != 200 {
		t.Fatal("same-origin logout", w.Code, w.Body.String())
	}
	if w := cookieRequest(t, h, "GET", "/api/v1/user/auth/session", "", cookie, "", nil); w.Code != 401 {
		t.Fatal("logged-out token reused")
	}
	if w := request(t, h, "GET", "/api/v1/user/auth/session", native, nil); w.Code != 200 {
		t.Fatal("logout revoked other device")
	}
	if w := request(t, h, "POST", "/api/v1/user/auth/password", native, map[string]any{"current_password": learnerPassword, "password": "new-user-password-123"}); w.Code != 200 {
		t.Fatal("password change", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/user/auth/session", native, nil); w.Code != 401 {
		t.Fatal("password change did not revoke all devices")
	}
	if w := request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": user.Email, "password": learnerPassword}); w.Code != 401 {
		t.Fatal("old password accepted")
	}
	if w := request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": user.Email, "password": "new-user-password-123"}); w.Code != 200 {
		t.Fatal("new password rejected", w.Code)
	}
}

func TestLegacyApplicationDataIsNotInheritedByUsers(t *testing.T) {
	service, h := app()
	ctx := context.Background()
	source, sourceKey := create(t, h, false)
	item := ingest(t, h, source.ID, sourceKey, "public article")
	old, key := managedClient(t, h, "old-shared-client", []string{"read", "feedback"})
	if _, err := service.ReportLearning(ctx, key, []study.LearningEventInput{{EventID: uuid.NewString(), ItemID: item.ID, Action: "view"}}); err != nil {
		t.Fatal(err)
	}
	user, token := managedLearner(t, h, "new-owner")
	var own study.LearningPage
	data(t, request(t, h, "GET", "/api/v1/client/learning", token, nil), &own)
	if own.ClientID != user.ID || own.Total != 0 {
		t.Fatal("legacy data reassigned", own)
	}
	var err error
	own, err = service.ClientLearning(ctx, key, "", nil, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if own.Total != 1 || own.ClientID != old.ID {
		t.Fatal("legacy data lost", own)
	}
	if w := request(t, h, "GET", "/api/v1/client/learning", key, nil); w.Code != 401 {
		t.Fatal("shared key still used as learner")
	}
}
