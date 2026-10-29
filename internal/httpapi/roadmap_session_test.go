package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceRoadmapBundleUsesWorkspaceAPIs(t *testing.T) {
	_, handler := app()
	response := request(t, handler, http.MethodGet, "/workspace/roadmaps.js", "", nil)
	if response.Code != http.StatusOK {
		t.Fatal("roadmap bundle unavailable", response.Code)
	}
	bundle := response.Body.String()
	if strings.Contains(bundle, "/admin/") {
		t.Fatal("user roadmap bundle still calls administrator endpoints, causing a 401 and a false session-expired screen")
	}
	for _, endpoint := range []string{"/workspace/roadmaps", "/workspace/items"} {
		if !strings.Contains(bundle, endpoint) {
			t.Fatalf("roadmap bundle missing user-workspace endpoint %s", endpoint)
		}
	}
	for _, key := range []string{adminKey, readKey, ""} {
		if response := request(t, handler, http.MethodGet, "/api/v1/workspace/roadmaps", key, nil); response.Code != http.StatusUnauthorized {
			t.Fatal("fix must not weaken the roadmap user boundary", response.Code)
		}
	}
}

func TestRoadmapAccessPreservesUserBrowserSession(t *testing.T) {
	_, handler := app()
	login := request(t, handler, http.MethodPost, "/api/v1/user/auth/login", "", map[string]any{"email": fixtureEmail, "password": learnerPassword})
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatal("user browser login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for _, access := range []struct {
		path string
		want int
	}{
		{"/api/v1/workspace/roadmaps", http.StatusOK},
		{"/api/v1/admin/roadmaps", http.StatusUnauthorized},
		{"/api/v1/user/auth/session", http.StatusOK},
		{"/api/v1/workspace/roadmaps", http.StatusOK},
	} {
		response := cookieRequest(t, handler, http.MethodGet, access.path, "", cookie, "", nil)
		if response.Code != access.want {
			t.Fatalf("%s: %d, expected %d", access.path, response.Code, access.want)
		}
	}
}
