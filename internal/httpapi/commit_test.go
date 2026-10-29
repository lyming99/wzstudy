package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"wzstudy/internal/config"
	"wzstudy/internal/study"
)

type lostCommitReplyStore struct{ study.Store }

func (s lostCommitReplyStore) Update(ctx context.Context, fn func(study.Tx) error) error {
	if e := s.Store.Update(ctx, fn); e != nil {
		return e
	}
	return errors.New("simulated lost commit acknowledgement")
}

func TestUncertainCommitDoesNotDeleteCommittedMedia(t *testing.T) {
	base := study.NewMemoryStore()
	seedTestWorkspace(study.NewService(base))
	service := study.NewService(lostCommitReplyStore{base})
	dir := t.TempDir()
	handler := New(config.Config{Env: "test", Store: "memory", AdminKey: adminKey, ReadKey: readKey, DataDir: dir, MaxUploadBytes: 1 << 20}, service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result := multipartRequest(t, handler, "/api/v1/workspace/uploads", workspaceKey, "committed.wav", wavBytes(100), study.ManagedInput{Status: "pending", Content: study.Content{Kind: "audio", Title: "Commit acknowledgement test"}})
	if result.Code != http.StatusInternalServerError {
		t.Fatal(result.Code, result.Body.String())
	}
	workspace, e := service.Workspace(testWorkspaceContext())
	if e != nil || len(workspace.Recent) != 1 {
		t.Fatal(workspace, e)
	}
	asset := workspace.Recent[0].Draft.Assets[0]
	if _, e = os.Stat(filepath.Join(dir, "uploads", asset.ID)); e != nil {
		t.Fatal("uncertain commit destroyed a referenced media file", e)
	}
	if read := request(t, handler, "GET", asset.URL, workspaceKey, nil); read.Code != 200 {
		t.Fatal("committed media became unreadable", read.Code)
	}
}
