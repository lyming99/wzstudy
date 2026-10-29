package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceHomeAssetsAreEmbedded(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	page := request(t, handler, http.MethodGet, "/workspace/", "", nil)
	if page.Code != http.StatusOK {
		t.Fatal("workspace page", page.Code)
	}
	body := page.Body.String()
	for _, expected := range []string{`data-route="home"`, `href="#home"`, `/workspace/home.css`, `/workspace/home.js`, `/workspace/app.js`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("workspace page missing %s", expected)
		}
	}
	if strings.Index(body, "/workspace/home.js") > strings.Index(body, "/workspace/app.js") {
		t.Fatal("home bundle must load before the main app")
	}
	for _, path := range []string{"/workspace/home.js", "/workspace/home.css"} {
		asset := request(t, handler, http.MethodGet, path, "", nil)
		if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
			t.Fatalf("missing home asset %s: %d", path, asset.Code)
		}
	}
}
