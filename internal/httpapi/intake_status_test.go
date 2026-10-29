package httpapi

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestIngestStatusUsesOnlyOwnIngestPermissionAndDoesNotWrite(t *testing.T) {
	service, handler, _ := managementApp(t, 1<<20)
	createClient := func(slug, kind string, permissions []string) (study.Source, string) {
		t.Helper()
		input := map[string]any{"slug": slug, "name": slug, "kind": kind}
		if kind == "rss" {
			input["feed_url"] = "https://example.org/feed.xml"
		} else {
			input["permissions"] = permissions
		}
		var out struct {
			Source study.Source `json:"source"`
			Key    string       `json:"access_key"`
		}
		data(t, request(t, handler, "POST", "/api/v1/workspace/sources", workspaceKey, input), &out)
		return out.Source, out.Key
	}
	collector, key := createClient("status-collector", "manual", []string{"ingest", "upload"})
	reader, readerSourceKey := createClient("status-no-ingest", "manual", []string{})
	other, otherKey := createClient("status-other", "manual", []string{"ingest"})
	rss, rssKey := createClient("status-rss", "rss", nil)
	path := "/api/v1/ingest/" + collector.ID + "/status"
	before, err := service.Source(testWorkspaceContext(), collector.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, "GET", path, key, nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var status struct {
		SourceID    string   `json:"source_id"`
		Enabled     bool     `json:"enabled"`
		AutoPublish bool     `json:"auto_publish"`
		Permissions []string `json:"permissions"`
	}
	data(t, response, &status)
	if status.SourceID != collector.ID || !status.Enabled || status.AutoPublish || !reflect.DeepEqual(status.Permissions, []string{"ingest", "upload"}) {
		t.Fatalf("incorrect own-source status: %+v", status)
	}
	for _, secret := range []string{key, study.HashKey(key), "access_key", "key_hash", "draft", "deliveries"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("status leaked a secret, content or inbox field")
		}
	}
	for _, tc := range []struct {
		name, path, key string
		want            int
	}{
		{"missing", path, "", 401},
		{"wrong", path, "invalid-source-key", 401},
		{"admin-is-not-source", path, adminKey, 401},
		{"shared-read-key-is-not-source", path, readKey, 401},
		{"other-source-key", path, otherKey, 401},
		{"other-source-id", "/api/v1/ingest/" + other.ID + "/status", key, 401},
		{"reader-without-ingest", "/api/v1/ingest/" + reader.ID + "/status", readerSourceKey, 403},
		{"rss-is-not-client-ingestion", "/api/v1/ingest/" + rss.ID + "/status", rssKey, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(t, handler, "GET", tc.path, tc.key, nil); got.Code != tc.want {
				t.Fatal(got.Code, tc.want)
			}
		})
	}
	// An ingest-only client also needs neither file upload nor inbox access.
	if got := request(t, handler, "GET", "/api/v1/ingest/"+other.ID+"/status", otherKey, nil); got.Code != 200 {
		t.Fatal("ingest-only status", got.Code)
	}
	after, err := service.Source(testWorkspaceContext(), collector.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("status changed source configuration or polling state", err)
	}
	_, total, err := service.AdminPage(testWorkspaceContext(), study.Filter{Limit: 100})
	if err != nil || total != 0 {
		t.Fatal("read-only status created materials", total, err)
	}
	changes, err := service.Changes(testWorkspaceContext(), 0, 100, nil)
	if err != nil || changes.HighWatermark != 0 {
		t.Fatal("read-only status emitted synchronization events", changes, err)
	}
	data(t, request(t, handler, "PUT", "/api/v1/workspace/sources/"+collector.ID, workspaceKey, map[string]any{
		"slug": collector.Slug, "name": collector.Name, "kind": collector.Kind,
		"enabled": false, "revision": collector.Revision, "permissions": collector.Permissions,
	}), &collector)
	if got := request(t, handler, "GET", path, key, nil); got.Code != 403 {
		t.Fatal("disabled source passed status authentication", got.Code)
	}
}
