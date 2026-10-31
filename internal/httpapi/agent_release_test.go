package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"wzstudy/internal/study"
)

func releaseUpload(t *testing.T, handler http.Handler, filename, version string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("version", version)
	_ = writer.WriteField("platform", "windows")
	_ = writer.WriteField("arch", "amd64")
	_ = writer.WriteField("notes", "测试版本")
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/agent/releases", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+adminKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAgentReleasePublishLifecycle(t *testing.T) {
	_, handler, dir := managementApp(t, 1<<20)
	payload := []byte("agent-binary-payload")
	created := releaseUpload(t, handler, "wzstudy-agent.exe", "0.1.0", payload)
	if created.Code != http.StatusCreated {
		t.Fatalf("upload release: %d %s", created.Code, created.Body.String())
	}
	var envelope struct {
		Data study.AgentRelease `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	release := envelope.Data
	if release.Version != "0.1.0" || release.Size != int64(len(payload)) || release.Filename != "wzstudy-agent.exe" {
		t.Fatalf("unexpected release: %+v", release)
	}
	stored := filepath.Join(dir, "agent-releases", release.ID)
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("package bytes are missing: %v", err)
	}

	var list []study.AgentRelease
	data(t, request(t, handler, http.MethodGet, "/api/v1/admin/agent/releases", adminKey, nil), &list)
	if len(list) != 1 || list[0].ID != release.ID {
		t.Fatalf("unexpected release list: %+v", list)
	}

	download := request(t, handler, http.MethodGet, "/api/v1/admin/agent/releases/"+release.ID+"/download", adminKey, nil)
	if download.Code != http.StatusOK || download.Body.String() != string(payload) {
		t.Fatalf("download failed: %d %q", download.Code, download.Body.String())
	}
	if header := download.Header().Get("Content-Disposition"); header == "" {
		t.Fatal("download must announce a filename")
	}

	if w := request(t, handler, http.MethodPost, "/api/v1/admin/agent/releases/"+release.ID+"/delete", adminKey, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete release: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatalf("package bytes must be removed: %v", err)
	}
}

func TestAgentReleaseRejectsInvalidUploadAndAnonymous(t *testing.T) {
	_, handler, _ := managementApp(t, 1<<20)
	if w := request(t, handler, http.MethodGet, "/api/v1/admin/agent/releases", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list must be rejected: %d", w.Code)
	}
	if w := releaseUpload(t, handler, "payload.sh", "0.2.0", []byte("#!/bin/sh")); w.Code != http.StatusBadRequest {
		t.Fatalf("unsupported extension must be rejected: %d %s", w.Code, w.Body.String())
	}
	if w := releaseUpload(t, handler, "agent.exe", "not-a-version", []byte("x")); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid version must be rejected: %d %s", w.Code, w.Body.String())
	}
}
