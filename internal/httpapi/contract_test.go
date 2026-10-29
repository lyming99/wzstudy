package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"wzstudy/docs"
)

func TestOpenAPICoversAllBusinessRoutes(t *testing.T) {
	_, h := app()
	raw, err := docs.Files.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.1.0" {
		t.Fatal(spec.OpenAPI)
	}
	seen := map[string]bool{}
	err = chi.Walk(h.(chi.Routes), func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1/") {
			return nil
		}
		path := strings.TrimPrefix(route, "/api/v1")
		if path == "/ingest/{id}" {
			path = "/ingest/{source_id}"
		}
		method = strings.ToLower(method)
		if _, exists := spec.Paths[path][method]; !exists {
			t.Errorf("undocumented route %s %s", method, path)
		}
		seen[method+" "+path] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, methods := range spec.Paths {
		for method := range methods {
			if !seen[method+" "+path] {
				t.Errorf("documented but missing route %s %s", method, path)
			}
		}
	}
	for _, path := range []string{"/docs", "/docs/management", "/openapi.json"} {
		w := request(t, h, "GET", path, "", nil)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal(path, w.Code)
		}
	}
}
func TestRejectsInvalidUTF8AndNullBodies(t *testing.T) {
	_, h := app()
	for _, raw := range [][]byte{[]byte("null"), []byte(`{"slug":"invalid","name":"` + string([]byte{0xff}) + `"}`)} {
		r := httptest.NewRequest("POST", "/api/v1/workspace/sources", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+workspaceKey)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/items", nil)
	r.Header.Set("Authorization", "bearer "+readKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("bearer auth scheme is case-insensitive", w.Code)
	}
}
