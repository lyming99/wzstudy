package collect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/tool"
)

func TestSkippedReceiptDoesNotConsumeNewItemQuota(t *testing.T) {
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"data":{"created":0,"updated":0,"unchanged":0,"skipped":1,"items":[]}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"created":1,"updated":0,"unchanged":0,"skipped":0,"items":[]}}`))
		}
	}))
	defer remote.Close()
	client, err := serverapi.New(remote.URL, "wzag-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	stats := &serverapi.Stats{}
	task := &serverapi.Task{ID: "task", Attempts: 1, Constraints: serverapi.Constraints{MaxItems: 1, DuplicatePolicy: "skip"}}
	ingestor := NewIngestor(client, task, 1, stats)
	call := tool.Call{Arguments: json.RawMessage(`{"items":[{"external_id":"candidate","title":"title"}]}`)}
	result, err := ingestor.Handle(context.Background(), call)
	if err != nil || !strings.Contains(result.Content, `"quota_left":1`) || stats.ItemsSkipped != 1 {
		t.Fatalf("skipped receipt: %+v %+v %v", result, stats, err)
	}
	_, err = ingestor.Handle(context.Background(), call)
	if err != nil || stats.ItemsCreated != 1 || stats.ItemsSkipped != 1 {
		t.Fatal("new item blocked", err, stats)
	}
	result, err = ingestor.Handle(context.Background(), call)
	if err != nil || !result.IsError || calls != 2 {
		t.Fatal("quota not enforced")
	}
}

func TestDuplicateCheckerUsesTaskBoundEndpoint(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/tasks/task-1/duplicates" || r.Method != "POST" {
			t.Error("wrong endpoint")
		}
		var in struct {
			Attempt int                            `json:"attempt"`
			Items   []serverapi.CollectionIdentity `json:"items"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Attempt != 2 || len(in.Items) != 1 {
			t.Error("wrong task scope")
		}
		_, _ = w.Write([]byte(`{"data":{"items":[{"external_id":"BV1oBxT6SECq","exists":true}]}}`))
	}))
	defer remote.Close()
	client, _ := serverapi.New(remote.URL, "wzag-test", "test")
	checker := NewDuplicateChecker(client, &serverapi.Task{ID: "task-1", Attempts: 2})
	result, err := checker.Handle(context.Background(), tool.Call{Arguments: json.RawMessage(`{"items":[{"external_id":"BV1oBxT6SECq","url":"https://www.bilibili.com/video/BV1oBxT6SECq"}]}`)})
	if err != nil || !strings.Contains(result.Content, `"exists":true`) {
		t.Fatal(result, err)
	}
}
