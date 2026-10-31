package study

import (
	"context"
	"errors"
	"testing"
)

func TestValidateTaskContentType(t *testing.T) {
	video := Content{Assets: []Asset{{MIME: "video/mp4"}}}
	audio := Content{Assets: []Asset{{MIME: "audio/mpeg"}}}
	article := Content{Kind: "article"}
	if err := validateTaskContentType("video", []Content{video}); err != nil {
		t.Fatalf("video item: %v", err)
	}
	if err := validateTaskContentType("video", []Content{article}); !errors.Is(err, ErrContentType) {
		t.Fatalf("video task without video asset: %v", err)
	}
	if err := validateTaskContentType("audio", []Content{video}); !errors.Is(err, ErrContentType) {
		t.Fatalf("audio task with video asset: %v", err)
	}
	if err := validateTaskContentType("audio", []Content{audio}); err != nil {
		t.Fatalf("audio item: %v", err)
	}
	if err := validateTaskContentType("article", []Content{article}); err != nil {
		t.Fatalf("article item: %v", err)
	}
	if err := validateTaskContentType("carousel", []Content{article}); !errors.Is(err, ErrContentType) {
		t.Fatalf("carousel task with article: %v", err)
	}
	if err := validateTaskContentType("", []Content{article}); err != nil {
		t.Fatalf("unrestricted task: %v", err)
	}
	if err := validateTaskContentType("unknown", []Content{video}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown content type: %v", err)
	}
}

func TestTaskContentTypeAndToolBudget(t *testing.T) {
	service := fixture(t, "memory")
	task, err := service.CreateCollectionTask(context.Background(), CollectionTaskInput{
		Goal:        "收集一个视频并保存视频文件",
		Constraints: TaskConstraints{ContentType: "video"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Constraints.ContentType != "video" {
		t.Fatalf("content type not stored: %+v", task.Constraints)
	}
	if task.Budget.MaxToolCalls != 100 {
		t.Fatalf("default tool call budget must be 100: %d", task.Budget.MaxToolCalls)
	}
	if _, err := service.CreateCollectionTask(context.Background(), CollectionTaskInput{
		Goal:        "非法类型",
		Constraints: TaskConstraints{ContentType: "document"},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown content type must be rejected: %v", err)
	}
}
