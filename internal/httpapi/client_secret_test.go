package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestClientSecretOwnerIsolationAndRestore(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	client, key := managedClient(t, h, "secret-owner", []string{"read", "sync"})
	_, otherToken := managedLearner(t, h, "client-secret-other")
	path := "/api/v1/workspace/clients/" + client.ID + "/key"
	for _, token := range []string{"", key, adminKey, legacyReadKey} {
		for _, method := range []string{"GET", "PUT"} {
			response := request(t, h, method, path, token, map[string]string{"access_key": key})
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("%s accepted non-user credential: %d", method, response.Code)
			}
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		response := request(t, h, method, path, otherToken, map[string]string{"access_key": key})
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), key) {
			t.Fatalf("%s cross-user leak: %d", method, response.Code)
		}
	}
	result := request(t, h, "GET", path, workspaceKey, nil)
	var secret struct {
		Available bool   `json:"available"`
		AccessKey string `json:"access_key"`
	}
	data(t, result, &secret)
	if !secret.Available || secret.AccessKey != key || !strings.Contains(result.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("owner cannot securely recover key")
	}
	for _, endpoint := range []string{"/api/v1/workspace/clients", "/api/v1/workspace/clients/" + client.ID, "/api/v1/workspace/workspace"} {
		out := request(t, h, "GET", endpoint, workspaceKey, nil)
		if strings.Contains(out.Body.String(), key) || strings.Contains(out.Body.String(), study.HashKey(key)) || strings.Contains(out.Body.String(), "key_ciphertext") {
			t.Fatal("secret in public client response", endpoint)
		}
	}
	legacyPath := "/api/v1/workspace/clients/22222222-2222-4222-8222-222222222222/key"
	data(t, request(t, h, "GET", legacyPath, workspaceKey, nil), &secret)
	if secret.Available || secret.AccessKey != "" {
		t.Fatal("hash-only key should be unavailable")
	}
	bad := request(t, h, "PUT", legacyPath, workspaceKey, map[string]string{"access_key": key})
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "client_key_mismatch") || strings.Contains(bad.Body.String(), key) {
		t.Fatal("wrong key not rejected safely", bad.Code)
	}
	restored := request(t, h, "PUT", legacyPath, workspaceKey, map[string]string{"access_key": readKey})
	if restored.Code != http.StatusOK || !strings.Contains(restored.Header().Get("Cache-Control"), "no-store") || strings.Contains(restored.Body.String(), readKey) {
		t.Fatal("valid key restore failed", restored.Code)
	}
	data(t, request(t, h, "GET", legacyPath, workspaceKey, nil), &secret)
	if !secret.Available || secret.AccessKey != readKey {
		t.Fatal("restored key unavailable")
	}
	if response := request(t, h, "GET", "/api/v1/client/connection", readKey, nil); response.Code != http.StatusOK {
		t.Fatal("restore changed live key", response.Code)
	}

	var rotated struct {
		Client study.Client `json:"client"`
		Key    string       `json:"access_key"`
	}
	rotation := request(t, h, "POST", path, workspaceKey, map[string]int64{"revision": client.Revision})
	data(t, rotation, &rotated)
	if !strings.Contains(rotation.Header().Get("Cache-Control"), "no-store") || rotated.Key == key {
		t.Fatal("invalid rotation")
	}
	data(t, request(t, h, "GET", path, workspaceKey, nil), &secret)
	if secret.AccessKey != rotated.Key {
		t.Fatal("saved secret did not follow rotation")
	}
	if out := request(t, h, "PUT", path, workspaceKey, map[string]string{"access_key": key}); out.Code != http.StatusBadRequest {
		t.Fatal("old key restored after rotation")
	}
	if out := request(t, h, "GET", "/api/v1/client/connection", key, nil); out.Code != http.StatusUnauthorized {
		t.Fatal("old key still authenticates")
	}
}
