package study

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteCollectionTask(t *testing.T) {
	service := fixture(t, "memory")
	ctx := context.Background()
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{Goal: "删除测试任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteCollectionTask(ctx, task.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("running task must not be deleted: %v", err)
	}
	if _, err := service.CancelAgentTask(ctx, task.ID, "测试取消"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteCollectionTask(ctx, task.ID); err != nil {
		t.Fatalf("delete cancelled task: %v", err)
	}
	if _, err := service.CollectionTask(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted task must be gone: %v", err)
	}
}
