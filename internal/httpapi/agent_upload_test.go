package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

func TestAgentUploadAsset(t *testing.T) {
	service, _, dir := managementApp(t, 1<<20)
	cfg := config.Config{Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: legacyReadKey, DataDir: dir, MaxUploadBytes: 1 << 20, AgentEnabled: true}
	handler := New(cfg, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	administrator, err := service.Administrator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	agent, key, err := service.CreateAgent(study.WithWorkspace(context.Background(), administrator.ID), study.AgentInput{Slug: "upload-agent", Name: "Upload Agent"})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("fake-upload-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/uploads", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload asset: %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data study.Asset `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ID == "" || envelope.Data.MIME != "text/plain" || envelope.Data.Size != int64(len("fake-upload-bytes")) {
		t.Fatalf("unexpected asset: %+v", envelope.Data)
	}
	if _, err := os.Stat(dir + "/uploads/" + envelope.Data.ID); err != nil {
		t.Fatalf("uploaded bytes missing: %v", err)
	}
	agentCtx := study.WithWorkspace(context.Background(), administrator.ID)
	source, _, err := service.CreateSource(agentCtx, study.SourceInput{Slug: "upload-source", Name: "Upload Source"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	task, err := service.CreateCollectionTask(agentCtx, study.CollectionTaskInput{
		SourceID:    source.ID,
		Goal:        "collect an uploaded article",
		Constraints: study.TaskConstraints{ContentType: "article"},
		AgentID:     agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	claimed, ok, err := service.ClaimAgentTask(agentCtx, agent.ID, 1)
	if err != nil || !ok {
		t.Fatalf("claim task: ok=%v err=%v", ok, err)
	}
	if claimed.ID != task.ID {
		t.Fatalf("claimed unexpected task: %s", claimed.ID)
	}
	receipt, err := service.IngestAgentTaskItems(agentCtx, agent.ID, claimed.ID, claimed.Attempts, []study.Content{{
		Kind:       "article",
		ExternalID: "uploaded-asset-test",
		Title:      "Uploaded asset test",
		Content:    "fake-upload-bytes",
		Assets:     []study.Asset{{ID: envelope.Data.ID}},
	}})
	if err != nil {
		t.Fatalf("ingest uploaded asset: %v", err)
	}
	if receipt.Created != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}
