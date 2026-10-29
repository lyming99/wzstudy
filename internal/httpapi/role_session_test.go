package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func roleCookie(t *testing.T, w *httptest.ResponseRecorder, name string, wantClear bool) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			if (c.MaxAge < 0) != wantClear {
				t.Fatalf("cookie %s clear=%v, want %v", name, c.MaxAge < 0, wantClear)
			}
			return c
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}

func roleRequest(t *testing.T, h http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestBrowserRoleSwitchRevokesOppositeSession(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	user, otherDevice := managedLearner(t, h, "role-switch")
	adminBody := map[string]string{"email": "admin@example.com", "password": "administrator-test-password"}
	userBody := map[string]string{"email": user.Email, "password": learnerPassword}

	adminLogin := roleRequest(t, h, "POST", "/api/v1/auth/login", adminBody)
	if adminLogin.Code != 200 {
		t.Fatal(adminLogin.Code, adminLogin.Body.String())
	}
	admin := roleCookie(t, adminLogin, userSessionCookie, false)
	bad := roleRequest(t, h, "POST", "/api/v1/user/auth/login", map[string]string{"email": user.Email, "password": "wrong"}, admin)
	if bad.Code != 401 || len(bad.Result().Cookies()) != 0 {
		t.Fatal("failed login changed browser session", bad.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, admin); w.Code != 200 {
		t.Fatal("failed login revoked admin", w.Code)
	}

	userLogin := roleRequest(t, h, "POST", "/api/v1/user/auth/login", userBody, admin)
	if userLogin.Code != 200 {
		t.Fatal(userLogin.Code, userLogin.Body.String())
	}
	browserUser := roleCookie(t, userLogin, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, admin); w.Code != 401 {
		t.Fatal("old admin session revived", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, browserUser); w.Code != 401 {
		t.Fatal("ordinary user accessed admin", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/workspace/workspace", nil, browserUser); w.Code != 200 {
		t.Fatal("user workspace denied", w.Code)
	}

	badAdmin := roleRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "wrong"}, browserUser)
	if badAdmin.Code != 401 || len(badAdmin.Result().Cookies()) != 0 {
		t.Fatal("failed admin login changed browser session", badAdmin.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/user/auth/session", nil, browserUser); w.Code != 200 {
		t.Fatal("failed admin login revoked user", w.Code)
	}
	adminLogin = roleRequest(t, h, "POST", "/api/v1/auth/login", adminBody, browserUser)
	if adminLogin.Code != 200 {
		t.Fatal(adminLogin.Code, adminLogin.Body.String())
	}
	admin = roleCookie(t, adminLogin, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/user/auth/session", nil, browserUser); w.Code != 401 {
		t.Fatal("old user session revived", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, admin); w.Code != 200 {
		t.Fatal("admin denied", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/workspace/workspace", nil, admin); w.Code != 200 {
		t.Fatal("administrator could not access its own workspace", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/user/auth/session", otherDevice, nil); w.Code != 200 {
		t.Fatal("role switch revoked another device", w.Code)
	}
}

func TestLegacyMixedCookiesAndTokenMode(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	user, _ := managedLearner(t, h, "mixed-role")
	adminLogin := roleRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	admin := roleCookie(t, adminLogin, userSessionCookie, false)
	legacy := &http.Cookie{Name: sessionCookie, Value: admin.Value}
	userLogin := roleRequest(t, h, "POST", "/api/v1/user/auth/login", map[string]string{"email": user.Email, "password": learnerPassword})
	browserUser := roleCookie(t, userLogin, userSessionCookie, false)

	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, legacy, browserUser); w.Code != 401 {
		t.Fatal("mixed cookies granted admin", w.Code)
	}
	keyRequest := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	keyRequest.AddCookie(legacy)
	keyRequest.AddCookie(browserUser)
	keyRequest.Header.Set("Authorization", "Bearer "+adminKey)
	keyResponse := httptest.NewRecorder()
	h.ServeHTTP(keyResponse, keyRequest)
	if keyResponse.Code != 200 {
		t.Fatal("explicit admin API key lost precedence over browser cookies", keyResponse.Code)
	}
	userSession := roleRequest(t, h, "GET", "/api/v1/user/auth/session", nil, legacy, browserUser)
	if userSession.Code != 200 {
		t.Fatal(userSession.Code, userSession.Body.String())
	}
	roleCookie(t, userSession, sessionCookie, true)
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, legacy); w.Code != 401 {
		t.Fatal("legacy admin cookie unexpectedly authorized", w.Code)
	}

	adminLogin = roleRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	admin = roleCookie(t, adminLogin, userSessionCookie, false)
	tokenLogin := roleRequest(t, h, "POST", "/api/v1/user/auth/login", map[string]string{"email": user.Email, "password": learnerPassword, "session_mode": "token"}, admin)
	if tokenLogin.Code != 200 || len(tokenLogin.Result().Cookies()) != 0 {
		t.Fatal("token login changed browser cookie", tokenLogin.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, admin); w.Code != 200 {
		t.Fatal("token login revoked admin cookie", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/admin/users", adminKey, nil); w.Code != 200 {
		t.Fatal("admin API key lost access", w.Code)
	}
}
