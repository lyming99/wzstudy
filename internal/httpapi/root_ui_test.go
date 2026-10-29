package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRootOpensWorkspaceHome(t *testing.T) {
	_, handler := app()

	root := request(t, handler, http.MethodGet, "/", "", nil)
	if root.Code != http.StatusTemporaryRedirect {
		t.Fatalf("root status = %d, want %d; body: %s", root.Code, http.StatusTemporaryRedirect, root.Body.String())
	}
	if location := root.Header().Get("Location"); location != "/workspace/" {
		t.Fatalf("root redirect = %q, want /workspace/", location)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/workspace/" {
		t.Fatalf("final response = %d %s, want 200 /workspace/", response.StatusCode, response.Request.URL.Path)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("final content type = %q, want text/html", contentType)
	}
	for _, expected := range []string{`data-route="home"`, `/workspace/home.js`, `/workspace/app.js`} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("workspace page missing %s", expected)
		}
	}
}
