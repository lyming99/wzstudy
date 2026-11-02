package study

import (
	"strings"
	"testing"
)

// Synthetic in-memory material only: prove plan jobs keep the existing
// claim/heartbeat/intake/complete contract without network collection.
func TestCollectionPlanExistingExecutionPipelineAndHistory(t *testing.T) {
	s, ctx, _ := agentFixture(t)
	plan := createPlan(t, s, ctx, "manual")
	run := triggerPlan(t, s, ctx, plan.ID, "pipeline-1")
	agent, _ := agentIdentity(t, s, ctx)
	task, ok, err := s.ClaimAgentTask(ctx, agent.ID, 1)
	if err != nil || !ok || task.ID != run.TaskID || task.PlanID != plan.ID || task.PlanRunID != run.ID || task.Deadline == nil {
		t.Fatal(task, ok, err)
	}
	if task.SourceID != plan.Task.SourceID || task.Budget != plan.Task.Budget || !strings.Contains(task.Goal, "本次计划触发时间") {
		t.Fatal("task binding, budget or dispatch context lost", task)
	}
	if _, err = s.HeartbeatAgentTask(ctx, agent.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.IngestAgentTaskItems(ctx, agent.ID, task.ID, task.Attempts, []Content{{
		ExternalID: "collection-pipeline-synthetic", Title: "Synthetic collection result", Kind: "article", Format: "text",
		URL: "https://go.dev/blog/synthetic-test", Content: "A synthetic in-memory test material, not a fetched article.",
	}})
	if err != nil || receipt.Created != 1 {
		t.Fatal(receipt, err)
	}
	if err = s.CompleteAgentTask(ctx, agent.ID, task.ID, task.Attempts, TaskStatusSucceeded, "synthetic pipeline complete", TaskStats{Rounds: 1, ToolCalls: 1}, nil); err != nil {
		t.Fatal(err)
	}
	runs, total, err := s.CollectionPlanRuns(ctx, plan.ID, 10, 0)
	if err != nil || total != 1 || runs[0].TaskStatus != TaskStatusSucceeded || runs[0].Stats.ItemsCreated != 1 || runs[0].Result.Summary != "synthetic pipeline complete" {
		t.Fatal(runs, total, err)
	}
	if _, err = s.DeleteCollectionTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	runs, _, err = s.CollectionPlanRuns(ctx, plan.ID, 10, 0)
	if err != nil || !runs[0].TaskDeleted || runs[0].Stats.ItemsCreated != 1 || runs[0].Result.Summary != "synthetic pipeline complete" {
		t.Fatal(runs, err)
	}
	next := triggerPlan(t, s, ctx, plan.ID, "pipeline-2")
	if next.TaskID == task.ID || next.ID == run.ID || next.DispatchStatus != "queued" {
		t.Fatal("rerun overwrote history", next)
	}
}
