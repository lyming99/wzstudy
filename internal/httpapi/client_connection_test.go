package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestQueryClientConnectionIdentity(t *testing.T) {
	s, h, _ := managementApp(t, 1<<20)
	_, sourceKey := create(t, h, false)
	for _, key := range []string{"", adminKey, legacyReadKey, sourceKey, "invalid-client-key"} {
		if out := request(t, h, http.MethodGet, "/api/v1/client/connection", key, nil); out.Code != 401 {
			t.Fatal("wrong credential accepted", out.Code)
		}
	}
	for _, tc := range []struct {
		name        string
		permissions []string
		want        int
	}{
		{"read", []string{"read"}, 200}, {"sync", []string{"sync"}, 200},
		{"both", []string{"read", "sync"}, 200}, {"none", []string{}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, key := managedClient(t, h, "connection-"+tc.name, tc.permissions)
			before, err := s.Client(testWorkspaceContext(), client.ID)
			if err != nil {
				t.Fatal(err)
			}
			out := request(t, h, "GET", "/api/v1/client/connection", key, nil)
			if out.Code != tc.want {
				t.Fatal(out.Code, tc.want)
			}
			if tc.want == 200 {
				var info map[string]json.RawMessage
				data(t, out, &info)
				if len(info) != 5 || string(info["client_id"]) != `"`+client.ID+`"` || string(info["type"]) != `"wzstudy.query_client"` || string(info["version"]) != "1" {
					t.Fatal("unexpected identity fields")
				}
				if !strings.Contains(out.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("cacheable identity")
				}
				if strings.Contains(out.Body.String(), key) || strings.Contains(out.Body.String(), study.HashKey(key)) || strings.Contains(out.Body.String(), "key_hash") || strings.Contains(out.Body.String(), "access_key") {
					t.Fatal("identity leaked credentials")
				}
			}
			after, err := s.Client(testWorkspaceContext(), client.ID)
			if err != nil || after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatal("identity request mutated client")
			}
		})
	}
	login := request(t, h, "POST", "/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "administrator-test-password"})
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing session cookie")
	}
	if out := browserRequest(t, h, "GET", "/api/v1/client/connection", "", cookies[0], nil); out.Code != 401 {
		t.Fatal("cookie substituted query key", out.Code)
	}
}

func TestQueryClientConnectionRevocationAndLegacyKey(t *testing.T) {
	s, h, _ := managementApp(t, 1<<20)
	client, key := managedClient(t, h, "connection-rotation", nil)
	var rotated struct {
		Client study.Client `json:"client"`
		Key    string       `json:"access_key"`
	}
	data(t, request(t, h, "POST", "/api/v1/workspace/clients/"+client.ID+"/key", workspaceKey, map[string]int64{"revision": client.Revision}), &rotated)
	if out := request(t, h, "GET", "/api/v1/client/connection", key, nil); out.Code != 401 {
		t.Fatal("old key retained", out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/client/connection", rotated.Key, nil); out.Code != 200 {
		t.Fatal(out.Code)
	}
	body := policyBody(rotated.Client, []string{"read", "sync"})
	body["enabled"] = false
	data(t, request(t, h, "PUT", "/api/v1/workspace/clients/"+client.ID, workspaceKey, body), &client)
	if out := request(t, h, "GET", "/api/v1/client/connection", rotated.Key, nil); out.Code != 403 {
		t.Fatal(out.Code)
	}
	body = policyBody(client, []string{})
	body["enabled"] = true
	data(t, request(t, h, "PUT", "/api/v1/workspace/clients/"+client.ID, workspaceKey, body), &client)
	if out := request(t, h, "GET", "/api/v1/client/connection", rotated.Key, nil); out.Code != 403 {
		t.Fatal(out.Code)
	}

	// A deliberately migrated old source prefix is valid only because its hash
	// now belongs to a Client. Prefix checks must not replace identity checks.
	source, legacyKey := create(t, h, false)
	if err := s.Store.Update(testWorkspaceContext(), func(tx study.Tx) error {
		v, e := tx.Source(source.ID)
		if e != nil {
			return e
		}
		v.Permissions = []string{study.PermissionRead}
		return tx.SaveSource(v)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateIdentities(testWorkspaceContext(), nil); err != nil {
		t.Fatal(err)
	}
	if out := request(t, h, "GET", "/api/v1/client/connection", legacyKey, nil); out.Code != 200 {
		t.Fatal("legacy query prefix rejected", out.Code)
	}
}

func TestQRCodeLocalStaticBundle(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	page := request(t, h, "GET", "/workspace/", "", nil)
	if !strings.Contains(page.Body.String(), "/workspace/qrcodegen.js") || strings.Index(page.Body.String(), "/workspace/qrcodegen.js") > strings.Index(page.Body.String(), "/workspace/app.js") {
		t.Fatal("QR encoder load order")
	}
	qr := request(t, h, "GET", "/workspace/qrcodegen.js", "", nil)
	if qr.Code != 200 || !strings.Contains(qr.Header().Get("Content-Type"), "javascript") || !strings.Contains(qr.Body.String(), "MIT License") {
		t.Fatal("local encoder missing")
	}
	if !strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("CSP weakened")
	}
}
