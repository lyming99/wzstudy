package study

import (
	"context"
	"errors"
	"testing"
)

func TestCollectionSkipDuplicatesPreservesDeletedAndDoesNotSpendQuota(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	source := agentSource(t, s, ctx)
	agent, _ := agentIdentity(t, s, ctx)
	// A prior BV tombstone must still block the same video under a URL or arbitrary ID.
	input := Content{ExternalID: "BV1oBxT6SECq", Title: "existing video", URL: "https://www.bilibili.com/video/BV1oBxT6SECq"}
	var tombstone Item
	if err := s.Store.Update(ctx, func(tx Tx) error {
		result, err := s.ingest(tx, source, []Content{input})
		if err != nil {
			return err
		}
		tombstone = result.Items[0]
		now := s.Now()
		tombstone.DeletedAt = &now
		tombstone.Published = nil
		return tx.SaveItem(tombstone)
	}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect new videos only", Constraints: TaskConstraints{MaxItems: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if task.Constraints.DuplicatePolicy != "skip" {
		t.Fatal("unsafe default")
	}
	if _, ok, err := s.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatal(err)
	}
	matches, err := s.CheckCollectionDuplicates(ctx, agent.ID, task.ID, 1, []CollectionIdentity{{ExternalID: "different-model-id", URL: input.URL + "/?tracking=1"}, {ExternalID: "BV1ufHN69EfW"}})
	if err != nil || !matches[0].Exists || matches[1].Exists {
		t.Fatalf("preflight %+v %v", matches, err)
	}
	duplicates := []Content{{ExternalID: input.URL, Title: "changed title", URL: input.URL}, {ExternalID: "made-up-id", Title: "changed title", URL: input.URL + "?x=1"}}
	receipt, err := s.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, duplicates)
	if err != nil || receipt.Skipped != 2 || receipt.Created != 0 || receipt.Updated != 0 || len(receipt.Items) != 0 {
		t.Fatalf("skip %+v %v", receipt, err)
	}
	var unchanged Item
	if err := s.Store.View(ctx, func(tx Tx) error {
		var err error
		unchanged, err = tx.FindItem(source.ID, input.ExternalID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if unchanged.DeletedAt == nil || unchanged.Revision != tombstone.Revision || unchanged.Draft.Title != input.Title {
		t.Fatal("tombstone changed")
	}
	receipt, err = s.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "new-1", Title: "New 1"}, {ExternalID: "new-2", Title: "New 2"}})
	if err != nil || receipt.Created != 2 {
		t.Fatalf("skips consumed quota: %+v %v", receipt, err)
	}
	stored, _ := s.CollectionTask(ctx, task.ID)
	if stored.Stats.ItemsSkipped != 2 || stored.Stats.ItemsCreated != 2 || stored.Stats.ItemsUpdated != 0 {
		t.Fatalf("wrong stats %+v", stored.Stats)
	}
	if _, err := s.CheckCollectionDuplicates(ctx, "another-agent", task.ID, 1, []CollectionIdentity{{ExternalID: input.ExternalID}}); !errors.Is(err, ErrTaskClaimed) {
		t.Fatal("other agent could check", err)
	}
	if _, err := s.CheckCollectionDuplicates(ctx, agent.ID, task.ID, 2, []CollectionIdentity{{ExternalID: input.ExternalID}}); !errors.Is(err, ErrLeaseExpired) {
		t.Fatal("wrong attempt accepted", err)
	}
}

func TestCollectionDuplicateScopeAndBatchNormalization(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	source := agentSource(t, s, ctx)
	agent, _ := agentIdentity(t, s, ctx)
	other, _, err := s.CreateSource(ctx, SourceInput{Slug: "other-source", Name: "Other source"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Update(ctx, func(tx Tx) error {
		_, e := s.ingest(tx, other, []Content{{ExternalID: "same", Title: "existing", URL: "https://example.com/item/same"}})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "new only", Constraints: TaskConstraints{MaxItems: 3}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.ClaimAgentTask(ctx, agent.ID, 1)
	check, err := s.CheckCollectionDuplicates(ctx, agent.ID, task.ID, 1, []CollectionIdentity{{ExternalID: "same", URL: "https://unrelated.example/item/same"}})
	if err != nil || check[0].Exists {
		t.Fatal("unrelated source-local ID incorrectly matched", err)
	}
	receipt, err := s.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "same", Title: "must not overwrite", URL: "https://example.com/item/same"}, {ExternalID: "BV1ufHN69EfW", Title: "new video"}, {ExternalID: "https://www.bilibili.com/video/BV1ufHN69EfW", Title: "batch duplicate"}})
	if err != nil || receipt.Created != 1 || receipt.Skipped != 2 {
		t.Fatalf("dedupe %+v %v", receipt, err)
	}
	foreign := WithWorkspace(context.Background(), "foreign-user")
	if _, err := s.CheckCollectionDuplicates(foreign, agent.ID, task.ID, 1, []CollectionIdentity{{ExternalID: "same"}}); err == nil {
		t.Fatal("workspace boundary failed", err)
	}
}

func TestMediaTaskDefaultBudgetAndExplicitBudget(t *testing.T) {
	for _, kind := range []string{"video", "audio", "article"} {
		in, err := normalizeTaskInput(CollectionTaskInput{Goal: "collect", Constraints: TaskConstraints{ContentType: kind}}, DefaultAgentPolicy())
		if err != nil {
			t.Fatal(err)
		}
		want := 1800
		if kind == "article" {
			want = 300
		}
		if in.Budget.TimeoutSeconds != want {
			t.Fatal(kind, in.Budget)
		}
		in, err = normalizeTaskInput(CollectionTaskInput{Goal: "collect", Constraints: TaskConstraints{ContentType: kind}, Budget: TaskBudget{TimeoutSeconds: 120}}, DefaultAgentPolicy())
		if err != nil || in.Budget.TimeoutSeconds != 120 {
			t.Fatal("explicit budget not honored")
		}
	}
}
