package study

import (
	"context"
	"testing"
)

func TestCreateCollectionTaskWithoutSource(t *testing.T) {
	service := fixture(t, "memory")
	ctx := context.Background()
	first, err := service.CreateCollectionTask(ctx, CollectionTaskInput{Goal: "收集 Go 官方博客的最新文章"})
	if err != nil {
		t.Fatalf("create task without source: %v", err)
	}
	if first.SourceID == "" {
		t.Fatal("task must be bound to an automatic source")
	}
	source, err := service.Source(ctx, first.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Slug != AgentCollectionSourceSlug || !source.Enabled || !source.Allows(PermissionIngest) || source.System {
		t.Fatalf("unexpected automatic source: %+v", source)
	}
	second, err := service.CreateCollectionTask(ctx, CollectionTaskInput{Goal: "再收集一篇文章"})
	if err != nil {
		t.Fatal(err)
	}
	if second.SourceID != first.SourceID {
		t.Fatalf("automatic source must be reused: %s vs %s", second.SourceID, first.SourceID)
	}
	sources, err := service.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range sources {
		if item.Slug == AgentCollectionSourceSlug {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("automatic source must be unique, got %d", count)
	}
}
