package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"wzstudy/internal/auth"
	"wzstudy/internal/study"
)

const fixtureUserID = "11111111-1111-4111-8111-111111111111"
const workspaceKey = "wzus_wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww"
const fixtureEmail = "workspace-fixture@example.test"

var fixtureHashOnce sync.Once
var fixturePasswordHash string

func testWorkspaceContext() context.Context {
	return study.WithWorkspace(context.Background(), fixtureUserID)
}

// Existing HTTP contract fixtures now exercise an authenticated user's workspace,
// not a system-administrator bypass. Account tests use real login independently.
func seedTestWorkspace(service *study.Service) {
	fixtureHashOnce.Do(func() { fixturePasswordHash, _ = auth.HashPassword(learnerPassword) })
	now := time.Now().UTC()
	user := study.User{ID: fixtureUserID, Email: fixtureEmail, DisplayName: "fixture user", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err := service.Store.Update(context.Background(), func(tx study.Tx) error {
		raw, _ := json.Marshal(map[string]any{"profile": user, "password_hash": fixturePasswordHash, "credential_version": 1})
		if err := tx.SaveRecord("learning_user", fixtureUserID, raw); err != nil {
			return err
		}
		raw, _ = json.Marshal(map[string]any{"id": study.HashKey(workspaceKey), "user_id": fixtureUserID, "credential_version": 1, "created_at": now, "expires_at": now.Add(8 * time.Hour)})
		return tx.SaveRecord("learning_user_session", study.HashKey(workspaceKey), raw)
	})
	if err != nil {
		panic(err)
	}
	err = service.Store.Update(testWorkspaceContext(), func(tx study.Tx) error {
		client := study.Client{OwnerID: fixtureUserID, ID: "22222222-2222-4222-8222-222222222222", Slug: "fixture-reader", Name: "fixture reader", Enabled: true, Permissions: []string{"read", "sync"}, Revision: 1, CreatedAt: now, UpdatedAt: now}
		raw, _ := json.Marshal(map[string]any{"client": client, "internal": map[string]any{"key_hash": study.HashKey(readKey)}})
		return tx.SaveRecord("client", client.ID, raw)
	})
	if err != nil {
		panic(err)
	}
}
func workspaceLearner(t *testing.T, h http.Handler, _ string) (study.User, string) {
	t.Helper()
	var session struct {
		User study.User `json:"user"`
	}
	data(t, request(t, h, "GET", "/api/v1/user/auth/session", workspaceKey, nil), &session)
	return session.User, workspaceKey
}

// Upload an explicit second copy into another user's own workspace, preserving
// event IDs to test per-user idempotency without granting cross-user materials.
func ownedPayload(t *testing.T, h http.Handler, token, original string, payload any) map[string]any {
	t.Helper()
	var content study.PublicItem
	data(t, request(t, h, "GET", "/api/v1/items/"+original, workspaceKey, nil), &content)
	var item study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items", token, study.ManagedInput{Status: "pending", Content: content.Content}), &item)
	data(t, request(t, h, "POST", "/api/v1/workspace/items/"+item.ID+"/publish", token, map[string]int64{"revision": item.Revision}), &item)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal([]byte(strings.ReplaceAll(string(raw), original, item.ID)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
