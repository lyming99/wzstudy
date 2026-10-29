package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmailLoginRejectsKeyAndDoesNotRevealAccount(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	known := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "not-the-password"})
	unknown := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "unknown@example.com", "password": "not-the-password"})
	if known.Code != 401 || unknown.Code != 401 || known.Body.String() != unknown.Body.String() {
		t.Fatal("login reveals account existence", known.Code, unknown.Code)
	}
	for _, w := range []*httptest.ResponseRecorder{known, unknown} {
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("failed login issued a session")
		}
	}
	if out := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"access_key": adminKey}); out.Code != 400 {
		t.Fatal("legacy key-only browser login accepted", out.Code)
	}
	if out := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": adminKey}); out.Code != 401 {
		t.Fatal("API key became a password", out.Code)
	}
	good := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": " ADMIN@EXAMPLE.COM ", "password": "administrator-test-password"})
	if good.Code != 200 || strings.Contains(good.Body.String(), "password_hash") || strings.Contains(good.Body.String(), "argon2id") {
		t.Fatal(good.Code, good.Body.String())
	}
	cookies := good.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	session := browserRequest(t, h, "GET", "/api/v1/auth/session", "", cookies[0], nil)
	if session.Code != 200 || !strings.Contains(session.Body.String(), "admin@example.com") {
		t.Fatal(session.Code, session.Body.String())
	}
	if strings.Contains(session.Body.String(), "password_hash") || strings.Contains(session.Body.String(), "argon2id") {
		t.Fatal("session exposes hash")
	}
	if out := request(t, h, "GET", "/api/v1/admin/users", adminKey, nil); out.Code != 200 {
		t.Fatal("admin API automation key lost compatibility", out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/admin/users", readKey, nil); out.Code != 401 {
		t.Fatal("read key became administrator")
	}
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/setup", "/api/v1/auth/initialize"} {
		if out := request(t, h, "POST", path, "", map[string]string{"email": "attacker@example.com", "password": "attacker-password"}); out.Code < 400 {
			t.Fatal("anonymous initialization exposed", path)
		}
	}
}

func TestEmailLoginBodyAndRateLimit(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	for i := 0; i < 10; i++ {
		out := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "bad"})
		if out.Code != 401 {
			t.Fatal(i, out.Code)
		}
	}
	out := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	if out.Code != 429 || out.Header().Get("Retry-After") != "60" {
		t.Fatal("missing login throttling", out.Code)
	}
	_, fresh, _ := managementApp(t, 1<<20)
	body := []byte(`{"email":"admin@example.com","password":"` + strings.Repeat("x", 5000) + `"}`)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	fresh.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("oversized login request accepted", w.Code)
	}
}

func TestEmailLoginHashConcurrencyIsBounded(t *testing.T) {
	s := &Server{loginBuckets: map[string]loginBucket{}, sessions: map[string]time.Time{}, passwordSlots: make(chan struct{}, 2)}
	s.passwordSlots <- struct{}{}
	s.passwordSlots <- struct{}{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	s.login(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("unbounded password hashing", w.Code)
	}
}
