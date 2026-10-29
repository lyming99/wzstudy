package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/auth"
	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

func TestUnifiedLoginDeterminesRoleAndPreservesSessionBoundaries(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	learner, otherDevice := managedLearner(t, h, "unified-login")
	adminBody := map[string]string{"email": " ADMIN@EXAMPLE.COM ", "password": "administrator-test-password"}
	userBody := map[string]string{"email": strings.ToUpper(learner.Email), "password": learnerPassword}

	admin := roleRequest(t, h, "POST", "/api/v1/auth/login", adminBody)
	var adminResult struct {
		Role          string              `json:"role"`
		Administrator study.Administrator `json:"administrator"`
	}
	data(t, admin, &adminResult)
	if adminResult.Role != "admin" || adminResult.Administrator.Email != "admin@example.com" {
		t.Fatal("administrator not classified by verified configured email", admin.Body.String())
	}
	adminCookie := roleCookie(t, admin, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, adminCookie); w.Code != http.StatusOK {
		t.Fatal("administrator denied management", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/workspace/workspace", nil, adminCookie); w.Code != http.StatusOK {
		t.Fatal("administrator denied their own workspace", w.Code)
	}

	bad := roleRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"email": learner.Email, "password": "wrong-password"}, adminCookie)
	if bad.Code != http.StatusUnauthorized || len(bad.Result().Cookies()) != 0 {
		t.Fatal("failed unified login changed the existing session", bad.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, adminCookie); w.Code != http.StatusOK {
		t.Fatal("failed login revoked existing admin", w.Code)
	}

	user := roleRequest(t, h, "POST", "/api/v1/auth/login", userBody, adminCookie)
	var userResult struct {
		Role string     `json:"role"`
		User study.User `json:"user"`
	}
	data(t, user, &userResult)
	if userResult.Role != "user" || userResult.User.ID != learner.ID {
		t.Fatal("learner login returned incorrect identity", user.Body.String())
	}
	userCookie := roleCookie(t, user, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, userCookie); w.Code != http.StatusUnauthorized {
		t.Fatal("ordinary user gained administrator access", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/workspace/workspace", nil, userCookie); w.Code != http.StatusOK {
		t.Fatal("learner workspace denied", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/user/auth/session", otherDevice, nil); w.Code != http.StatusOK {
		t.Fatal("unified login revoked a different device", w.Code)
	}
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, adminCookie); w.Code != http.StatusUnauthorized {
		t.Fatal("old administrator session survived role switch", w.Code)
	}
	admin = roleRequest(t, h, "POST", "/api/v1/auth/login", adminBody, userCookie)
	data(t, admin, &adminResult)
	roleCookie(t, admin, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/user/auth/session", nil, userCookie); w.Code != http.StatusUnauthorized {
		t.Fatal("old learner browser session survived role switch", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/auth/login", "", map[string]any{"email": learner.Email, "password": learnerPassword, "role": "admin"}); w.Code != http.StatusBadRequest {
		t.Fatal("client supplied role was accepted", w.Code)
	}
}

func TestUnifiedLoginRejectsDisabledAndMismatchedConfiguration(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	var user study.User
	data(t, request(t, h, "POST", "/api/v1/admin/users", adminKey, map[string]any{"email": "disabled@example.test", "display_name": "disabled", "password": learnerPassword, "enabled": false}), &user)
	if w := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": user.Email, "password": learnerPassword}); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatal("disabled user logged in", w.Code)
	}
	cfg := config.Config{Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: legacyReadKey, SystemAdminEmail: "different@example.test"}
	mismatch := New(cfg, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := request(t, mismatch, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "administrator_configuration_mismatch") || len(w.Result().Cookies()) != 0 {
		t.Fatal("mismatched configuration was accepted", w.Code, w.Body.String())
	}
}

func TestUnifiedLoginPreservesLegacySameEmailWithoutPrivilegeEscalation(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	if w := request(t, h, "POST", "/api/v1/admin/users", adminKey, map[string]any{"email": "ADMIN@example.com", "display_name": "duplicate", "password": learnerPassword}); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "email_conflict") {
		t.Fatal("new duplicate administrator email was accepted", w.Code, w.Body.String())
	}
	// Model a pre-existing legacy collision directly. It must never be merged,
	// renamed, deleted, or promoted just because the emails match.
	id := uuid.NewString()
	hash, err := auth.HashPassword(learnerPassword)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	legacy := study.User{ID: id, Email: "admin@example.com", DisplayName: "legacy learner", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	raw, err := json.Marshal(map[string]any{"profile": legacy, "password_hash": hash, "credential_version": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Update(context.Background(), func(tx study.Tx) error { return tx.SaveRecord("learning_user", id, raw) }); err != nil {
		t.Fatal(err)
	}
	item, err := service.SaveManaged(study.WithWorkspace(context.Background(), id), "", study.ManagedInput{Status: "draft", Content: study.Content{Title: "legacy owned item", Content: "must stay with the same user", Format: "text"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	user := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": learnerPassword})
	var result struct {
		Role string     `json:"role"`
		User study.User `json:"user"`
	}
	data(t, user, &result)
	if result.Role != "user" || result.User.ID != id {
		t.Fatal("legacy user identity was changed")
	}
	cookie := roleCookie(t, user, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/admin/users", nil, cookie); w.Code != http.StatusUnauthorized {
		t.Fatal("legacy learner acquired administrator permission", w.Code)
	}
	admin := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	if admin.Code != http.StatusOK {
		t.Fatal("existing administrator lost access", admin.Code)
	}
	adminCookie := roleCookie(t, admin, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/workspace/items/"+item.ID, nil, adminCookie); w.Code != http.StatusNotFound {
		t.Fatal("same-email administrator gained the learner's workspace", w.Code)
	}
	if after, err := service.User(context.Background(), id); err != nil || after.ID != legacy.ID || after.Email != legacy.Email {
		t.Fatal("legacy learner data changed during login", after, err)
	}
	// Existing collisions remain manageable, then can be resolved by changing
	// the learner email without changing its owner ID or personal data.
	var updated study.User
	data(t, request(t, h, "PUT", "/api/v1/admin/users/"+id, adminKey, map[string]any{"revision": legacy.Revision, "email": legacy.Email, "display_name": "updated learner"}), &updated)
	if updated.ID != id || updated.Email != legacy.Email {
		t.Fatal("existing duplicate could not be safely edited")
	}
	data(t, request(t, h, "PUT", "/api/v1/admin/users/"+id, adminKey, map[string]any{"revision": updated.Revision, "email": "resolved@example.test", "display_name": updated.DisplayName}), &updated)
	resolved := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": updated.Email, "password": learnerPassword})
	data(t, resolved, &result)
	if result.Role != "user" || result.User.ID != id {
		t.Fatal("renamed learner lost its identity", resolved.Body.String())
	}
	resolvedCookie := roleCookie(t, resolved, userSessionCookie, false)
	if w := roleRequest(t, h, "GET", "/api/v1/workspace/items/"+item.ID, nil, resolvedCookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "legacy owned item") {
		t.Fatal("legacy learner lost personal data after conflict resolution", w.Code, w.Body.String())
	}
}
