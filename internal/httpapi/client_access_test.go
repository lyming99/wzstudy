package httpapi

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func managedClient(t *testing.T, h http.Handler, slug string, permissions []string) (study.Client, string) {
	t.Helper()
	var out struct {
		Client study.Client `json:"client"`
		Key    string       `json:"access_key"`
	}
	data(t, request(t, h, "POST", "/api/v1/workspace/clients", workspaceKey, map[string]any{"slug": slug, "name": slug, "permissions": permissions}), &out)
	want := permissions
	if want == nil {
		want = []string{"read", "sync"}
	}
	if !strings.HasPrefix(out.Key, "wzck_") || !reflect.DeepEqual(out.Client.Permissions, want) {
		t.Fatal(out.Client, out.Key != "")
	}
	return out.Client, out.Key
}
func policyBody(v study.Client, permissions []string) map[string]any {
	return map[string]any{"slug": v.Slug, "name": v.Name, "description": v.Description, "enabled": v.Enabled, "revision": v.Revision, "permissions": permissions}
}

func TestManagedClientPermissionMatrix(t *testing.T) {
	s, h, _ := managementApp(t, 1<<20)
	var audio study.Item
	data(t, multipartRequest(t, h, "/api/v1/workspace/uploads", workspaceKey, "sample.wav", wavBytes(120), study.ManagedInput{Status: "pending", Content: study.Content{Kind: "audio", Title: "published audio"}}), &audio)
	data(t, request(t, h, "POST", "/api/v1/workspace/items/"+audio.ID+"/publish", workspaceKey, map[string]int64{"revision": audio.Revision}), &audio)
	private, e := s.SaveManaged(testWorkspaceContext(), "", study.ManagedInput{Status: "draft", Content: study.Content{Title: "private draft", Content: "must not leak"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	source, sourceKey := create(t, h, false)
	for _, tc := range []struct {
		name        string
		permissions []string
		read, sync  bool
	}{
		{"none", []string{}, false, false}, {"reader", []string{"read"}, true, false}, {"syncer", []string{"sync"}, false, true}, {"default", nil, true, true}, {"both", []string{"read", "sync"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, key := managedClient(t, h, tc.name, tc.permissions)
			for _, path := range []string{"/api/v1/sources", "/api/v1/items", "/api/v1/items/" + audio.ID, audio.Published.Assets[0].URL} {
				want := 403
				if tc.read {
					want = 200
				}
				if out := request(t, h, "GET", path, key, nil); out.Code != want {
					t.Fatal(path, out.Code, want)
				}
			}
			if tc.read {
				if out := request(t, h, "GET", "/api/v1/items/"+private.ID, key, nil); out.Code != 404 {
					t.Fatal(out.Code)
				}
				if out := request(t, h, "GET", "/api/v1/items", key, nil); strings.Contains(out.Body.String(), "must not leak") {
					t.Fatal("draft leaked")
				}
			}
			want := 403
			if tc.sync {
				want = 200
			}
			if out := request(t, h, "GET", "/api/v1/changes", key, nil); out.Code != want {
				t.Fatal(out.Code, want)
			}
			for _, path := range []string{"/api/v1/ingest/" + source.ID, "/api/v1/ingest/" + source.ID + "/files"} {
				if out := request(t, h, "POST", path, key, map[string]any{}); out.Code != 401 {
					t.Fatal("query key entered ingestion", out.Code)
				}
			}
			for _, path := range []string{"/api/v1/workspace/workspace", "/api/v1/workspace/clients", "/api/v1/workspace/sources", "/api/v1/auth/session"} {
				if out := request(t, h, "GET", path, key, nil); out.Code != 401 {
					t.Fatal(path, out.Code)
				}
			}
		})
	}
	for _, path := range []string{"/api/v1/items", "/api/v1/changes", audio.Published.Assets[0].URL} {
		if out := request(t, h, "GET", path, sourceKey, nil); out.Code != 401 {
			t.Fatal("source key queried", path, out.Code)
		}
	}
	if out := request(t, h, "POST", "/api/v1/ingest/"+source.ID, sourceKey, map[string]any{"items": []study.Content{{ExternalID: "own", Title: "allowed"}}}); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if out := request(t, h, "POST", "/api/v1/workspace/sources", workspaceKey, map[string]any{"slug": "bad", "name": "bad", "permissions": []string{"read"}}); out.Code != 400 {
		t.Fatal("source granted query", out.Code)
	}
	if out := request(t, h, "POST", "/api/v1/workspace/clients", workspaceKey, map[string]any{"slug": "bad", "name": "bad", "permissions": []string{"ingest"}}); out.Code != 400 {
		t.Fatal("client granted ingest", out.Code)
	}
}

func TestClientPolicyManagementIsolationAndRotation(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	permissions := []string{"read", "sync"}
	a, keyA := managedClient(t, h, "client-a", permissions)
	b, keyB := managedClient(t, h, "client-b", permissions)
	if keyA == keyB {
		t.Fatal("shared keys")
	}
	for _, otherKey := range []string{keyA, readKey, "wrong-key"} {
		for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/workspace/clients"}, {"PUT", "/api/v1/workspace/clients/" + b.ID}, {"POST", "/api/v1/workspace/clients/" + b.ID + "/key"}} {
			if out := request(t, h, tc.method, tc.path, otherKey, policyBody(b, []string{})); out.Code != 401 {
				t.Fatal(out.Code)
			}
		}
	}
	out := request(t, h, "GET", "/api/v1/workspace/clients", workspaceKey, nil)
	if strings.Contains(out.Body.String(), keyA) || strings.Contains(out.Body.String(), keyB) || strings.Contains(out.Body.String(), study.HashKey(keyA)) || strings.Contains(out.Body.String(), "access_key") {
		t.Fatal("secret leaked")
	}
	body := policyBody(a, []string{"admin"})
	if out := request(t, h, "PUT", "/api/v1/workspace/clients/"+a.ID, workspaceKey, body); out.Code != 400 {
		t.Fatal(out.Code)
	}
	body = policyBody(a, []string{})
	body["revision"] = a.Revision - 1
	if out := request(t, h, "PUT", "/api/v1/workspace/clients/"+a.ID, workspaceKey, body); out.Code != 409 {
		t.Fatal(out.Code)
	}
	data(t, request(t, h, "PUT", "/api/v1/workspace/clients/"+a.ID, workspaceKey, policyBody(a, []string{})), &a)
	for _, path := range []string{"/api/v1/items", "/api/v1/changes"} {
		if out := request(t, h, "GET", path, keyA, nil); out.Code != 403 {
			t.Fatal(out.Code)
		}
	}
	body = policyBody(a, nil)
	delete(body, "permissions")
	data(t, request(t, h, "PUT", "/api/v1/workspace/clients/"+a.ID, workspaceKey, body), &a)
	if a.Permissions == nil || len(a.Permissions) != 0 {
		t.Fatal("omitted restored policy")
	}
	data(t, request(t, h, "PUT", "/api/v1/workspace/clients/"+a.ID, workspaceKey, policyBody(a, permissions)), &a)
	var rotated struct {
		Client study.Client `json:"client"`
		Key    string       `json:"access_key"`
	}
	data(t, request(t, h, "POST", "/api/v1/workspace/clients/"+a.ID+"/key", workspaceKey, map[string]int64{"revision": a.Revision}), &rotated)
	if out := request(t, h, "GET", "/api/v1/items", keyA, nil); out.Code != 401 {
		t.Fatal(out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/items", rotated.Key, nil); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if out := request(t, h, "GET", "/api/v1/items", keyB, nil); out.Code != 200 {
		t.Fatal(out.Code)
	}
	a = rotated.Client
	body = policyBody(a, permissions)
	body["enabled"] = false
	data(t, request(t, h, "PUT", "/api/v1/workspace/clients/"+a.ID, workspaceKey, body), &a)
	for _, path := range []string{"/api/v1/items", "/api/v1/changes"} {
		if out := request(t, h, "GET", path, rotated.Key, nil); out.Code != 403 {
			t.Fatal(out.Code)
		}
	}
}
