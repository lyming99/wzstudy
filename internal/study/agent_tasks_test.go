package study

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func agentFixture(t *testing.T) (*Service, context.Context, string) {
	t.Helper()
	service := NewService(NewMemoryStore())
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return ref }
	user := User{ID: uuid.NewString(), Email: "agent-fixture@example.com", DisplayName: "agent fixture", Enabled: true, Revision: 1, CreatedAt: ref, UpdatedAt: ref}
	if err := service.Store.Update(context.Background(), func(tx Tx) error {
		return recordSave(tx, "learning_user", user.ID, userRecord{Profile: user})
	}); err != nil {
		t.Fatalf("seed workspace user: %v", err)
	}
	return service, WithWorkspace(context.Background(), user.ID), user.ID
}

func agentSource(t *testing.T, service *Service, ctx context.Context) Source {
	t.Helper()
	source, _, err := service.CreateSource(ctx, SourceInput{Slug: "agent-source", Name: "Agent Source"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	return source
}

func agentIdentity(t *testing.T, service *Service, ctx context.Context) (Agent, string) {
	t.Helper()
	agent, key, err := service.CreateAgent(ctx, AgentInput{Slug: "collector", Name: "Collector"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent, key
}

func TestAgentIdentityAndKeyAccess(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	agent, key := agentIdentity(t, service, ctx)
	if agent.OwnerID == "" || len(agent.Permissions) != 2 {
		t.Fatalf("unexpected agent: %+v", agent)
	}
	if !keyHasPrefix(key, "wzag_") {
		t.Fatalf("agent key prefix: %q", key)
	}
	resolved, err := service.AgentForKey(context.Background(), key, PermissionTaskClaim)
	if err != nil || resolved.ID != agent.ID {
		t.Fatalf("resolve agent key: %v", err)
	}
	if _, err := service.AgentForKey(context.Background(), key, PermissionRead); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent must not hold read permission: %v", err)
	}
	if _, err := service.AgentForKey(context.Background(), "wzag_wrong", PermissionTaskClaim); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown key must be unauthorized: %v", err)
	}
	if _, _, err := service.CreateAgent(ctx, AgentInput{Slug: "collector", Name: "duplicate"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug must conflict: %v", err)
	}
	if _, err := service.UpdateAgent(ctx, agent.ID, agent.Revision, AgentInput{Slug: agent.Slug, Name: "Collector", Enabled: boolPointer(false)}); err != nil {
		t.Fatalf("disable agent: %v", err)
	}
	if _, err := service.AgentForKey(context.Background(), key, PermissionTaskClaim); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled agent must be rejected: %v", err)
	}
}

func keyHasPrefix(key, prefix string) bool {
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}

func boolPointer(value bool) *bool { return &value }

func TestCollectionTaskRequiresIngestSource(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	if _, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: uuid.NewString(), Goal: "collect"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source: %v", err)
	}
	source := agentSource(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "采集 Go 官方博客近三篇文章"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != TaskStatusPending || task.Constraints.MaxItems != service.AgentPolicy.DefaultMaxItems {
		t.Fatalf("unexpected task defaults: %+v", task)
	}
	if task.Budget.TimeoutSeconds != 300 || task.Budget.MaxRounds != 100 || task.Budget.MaxToolCalls != 100 || task.MaxAttempts != 3 {
		t.Fatalf("unexpected budget defaults: %+v", task.Budget)
	}
	if _, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect", Constraints: TaskConstraints{MaxItems: 1000}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("quota above policy must be rejected: %v", err)
	}
}

func TestCollectionTaskBudgetBounds(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{
		SourceID: source.ID,
		Goal:     "collect",
		Budget:   TaskBudget{MaxRounds: 1000, MaxToolCalls: 1000, TimeoutSeconds: 300},
	})
	if err != nil {
		t.Fatalf("maximum budget must be accepted: %v", err)
	}
	if task.Budget.MaxRounds != 1000 || task.Budget.MaxToolCalls != 1000 {
		t.Fatalf("maximum budget was altered: %+v", task.Budget)
	}
	for _, budget := range []TaskBudget{
		{MaxRounds: 1001, MaxToolCalls: 100, TimeoutSeconds: 300},
		{MaxRounds: 100, MaxToolCalls: 1001, TimeoutSeconds: 300},
	} {
		if _, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect", Budget: budget}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("budget above maximum must be rejected: %+v: %v", budget, err)
		}
	}
}

func TestAgentUploadedVideoAssetBindsOnIngest(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	agent, _ := agentIdentity(t, service, ctx)
	if _, err := service.CreateCollectionTask(ctx, CollectionTaskInput{
		SourceID:    source.ID,
		Goal:        "collect a video",
		Constraints: TaskConstraints{ContentType: "video"},
	}); err != nil {
		t.Fatalf("create video task: %v", err)
	}
	claimed, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1)
	if err != nil || !ok {
		t.Fatalf("claim task: ok=%v err=%v", ok, err)
	}
	asset := Asset{ID: uuid.NewString(), Name: "video.mp4", MIME: "video/mp4", Size: 1024, CreatedAt: service.Now()}
	if err := service.RegisterUploadedAsset(ctx, asset); err != nil {
		t.Fatalf("register uploaded asset: %v", err)
	}
	receipt, err := service.IngestAgentTaskItems(ctx, agent.ID, claimed.ID, claimed.Attempts, []Content{{
		Kind:       "video",
		ExternalID: "bv-uploaded-video",
		Title:      "uploaded video",
		Assets:     []Asset{{ID: asset.ID}},
	}})
	if err != nil {
		t.Fatalf("ingest uploaded video: %v", err)
	}
	if receipt.Created != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	var stored Asset
	if err := service.Store.View(ctx, func(tx Tx) error {
		var e error
		stored, e = recordAs[Asset](tx, "asset", asset.ID)
		return e
	}); err != nil {
		t.Fatalf("read registered asset: %v", err)
	}
	if stored.SourceID != source.ID {
		t.Fatalf("asset was not bound to the task source: %+v", stored)
	}
}

func TestClaimIsExclusiveAcrossAgents(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	first, _ := agentIdentity(t, service, ctx)
	second, _, err := service.CreateAgent(ctx, AgentInput{Slug: "collector-two", Name: "Collector Two"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	var winners int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for index := 0; index < 8; index++ {
		agentID := first.ID
		if index%2 == 1 {
			agentID = second.ID
		}
		wg.Add(1)
		go func(agentID string) {
			defer wg.Done()
			claimed, ok, err := service.ClaimAgentTask(ctx, agentID, 1)
			if err != nil || !ok {
				return
			}
			if claimed.ID != task.ID {
				t.Errorf("unexpected task claimed: %s", claimed.ID)
				return
			}
			mu.Lock()
			winners++
			mu.Unlock()
		}(agentID)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("exactly one agent may claim, got %d", winners)
	}
	stored, err := service.CollectionTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != TaskStatusClaimed || stored.Attempts != 1 || stored.AgentID == "" {
		t.Fatalf("unexpected claimed task: %+v", stored)
	}
}

func TestExpiredLeaseIsReclaimedWithAttemptIncrement(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	agent, _ := agentIdentity(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatalf("first claim: %v", err)
	}
	service.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC) }
	reclaimed, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1)
	if err != nil || !ok {
		t.Fatalf("reclaim expired lease: %v", err)
	}
	if reclaimed.ID != task.ID || reclaimed.Attempts != 2 || reclaimed.Status != TaskStatusClaimed {
		t.Fatalf("unexpected reclaimed task: %+v", reclaimed)
	}
	service.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC) }
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatalf("third attempt: %v", err)
	}
	service.Now = func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || ok {
		t.Fatalf("attempts must be exhausted: ok=%v err=%v", ok, err)
	}
	stored, err := service.CollectionTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != TaskStatusExpired || stored.Result == nil || stored.Result.Error.Code != "attempts_exhausted" {
		t.Fatalf("unexpected expired task: %+v", stored)
	}
}

func TestAgentTaskItemsQuotaDomainsAndIdempotency(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	agent, _ := agentIdentity(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{
		SourceID:    source.ID,
		Goal:        "collect",
		Constraints: TaskConstraints{MaxItems: 2, DuplicatePolicy: "update", AllowDomains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatalf("claim: %v", err)
	}
	if _, err := service.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "x", Title: "X", URL: "https://evil.test/x"}}); !errors.Is(err, ErrDomain) {
		t.Fatalf("domain allowlist: %v", err)
	}
	receipt, err := service.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "a1", Title: "A1", Format: "markdown", Content: "body", URL: "https://blog.example.com/a1"}})
	if err != nil || receipt.Created != 1 || receipt.TaskID != task.ID || receipt.Integration != "agent" {
		t.Fatalf("first push: %+v %v", receipt, err)
	}
	second, err := service.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "a1", Title: "A1 updated", Format: "markdown", Content: "body2", URL: "https://blog.example.com/a1"}})
	if err != nil || second.Updated != 1 {
		t.Fatalf("idempotent update: %+v %v", second, err)
	}
	if _, err := service.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "a2", Title: "A2", URL: "https://example.com/a2"}}); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota must be enforced: %v", err)
	}
	if _, err := service.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "b1", Title: "B1"}}); !errors.Is(err, ErrQuota) {
		t.Fatalf("item without URL must not bypass an allowlist: %v", err)
	}
	stored, err := service.CollectionTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Stats.ItemsCreated != 1 || stored.Stats.ItemsUpdated != 1 {
		t.Fatalf("task stats: %+v", stored.Stats)
	}
	history, err := service.IntakeHistory(ctx, source.ID, 10, 0)
	if err != nil || history.Total != 2 || history.Items[0].TaskID != task.ID {
		t.Fatalf("intake history: %+v %v", history, err)
	}
}

func TestCompleteIsIdempotentAndLeaseBlocksForeignWrites(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	agent, _ := agentIdentity(t, service, ctx)
	other, _, err := service.CreateAgent(ctx, AgentInput{Slug: "collector-other", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatalf("claim: %v", err)
	}
	if err := service.CompleteAgentTask(ctx, other.ID, task.ID, 1, TaskStatusSucceeded, "done", TaskStats{}, nil); !errors.Is(err, ErrTaskClaimed) {
		t.Fatalf("foreign agent must not complete: %v", err)
	}
	if _, err := service.HeartbeatAgentTask(ctx, other.ID, task.ID); !errors.Is(err, ErrTaskClaimed) {
		t.Fatalf("foreign agent must not heartbeat: %v", err)
	}
	if err := service.CompleteAgentTask(ctx, agent.ID, task.ID, 1, TaskStatusSucceeded, "采集完成", TaskStats{Rounds: 3, ToolCalls: 4}, nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := service.CompleteAgentTask(ctx, agent.ID, task.ID, 1, TaskStatusFailed, "rewrite", TaskStats{}, nil); err != nil {
		t.Fatalf("second completion must be a no-op: %v", err)
	}
	stored, err := service.CollectionTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != TaskStatusSucceeded || stored.Result.Summary != "采集完成" || stored.Stats.Rounds != 3 {
		t.Fatalf("terminal task must keep its first result: %+v", stored)
	}
	if _, err := service.IngestAgentTaskItems(ctx, agent.ID, task.ID, 1, []Content{{ExternalID: "late", Title: "Late"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal task must reject pushes: %v", err)
	}
}

func TestCancelAndRetryLifecycle(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	agent, _ := agentIdentity(t, service, ctx)
	pendingTask, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "pending cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelAgentTask(ctx, pendingTask.ID, "不再需要"); err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.CollectionTask(ctx, pendingTask.ID)
	if err != nil || cancelled.Status != TaskStatusCancelled {
		t.Fatalf("pending cancel: %+v %v", cancelled, err)
	}
	if _, err := service.RetryAgentTask(ctx, pendingTask.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := service.CollectionTask(ctx, pendingTask.ID)
	if err != nil || retried.Status != TaskStatusPending || retried.Attempts != 0 || retried.Result != nil {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	claimedTask, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "claimed cancel", Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatalf("claim: %v", err)
	}
	if _, err := service.CancelAgentTask(ctx, claimedTask.ID, "立即停止"); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := service.HeartbeatAgentTask(ctx, agent.ID, claimedTask.ID)
	if err != nil || !heartbeat.CancelRequested {
		t.Fatalf("cancel signal: %+v %v", heartbeat, err)
	}
	if _, err := service.IngestAgentTaskItems(ctx, agent.ID, claimedTask.ID, 1, []Content{{ExternalID: "c1", Title: "C1"}}); !errors.Is(err, ErrTaskCancelled) {
		t.Fatalf("cancelled task must reject pushes: %v", err)
	}
	if err := service.ReleaseAgentTask(ctx, agent.ID, claimedTask.ID, 1, "本机无法继续"); err != nil {
		t.Fatal(err)
	}
	released, err := service.CollectionTask(ctx, claimedTask.ID)
	if err != nil || released.Status != TaskStatusCancelled {
		t.Fatalf("release of cancelled task must stay terminal: %+v %v", released, err)
	}
}

func TestHeartbeatAllowsGraceWindow(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	service.AgentPolicy.LeaseSeconds = 60
	service.AgentPolicy.HeartbeatGraceSeconds = 30
	source := agentSource(t, service, ctx)
	agent, _ := agentIdentity(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.ClaimAgentTask(ctx, agent.ID, 1); err != nil || !ok {
		t.Fatalf("claim: %v", err)
	}
	service.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 1, 20, 0, time.UTC) }
	heartbeat, err := service.HeartbeatAgentTask(ctx, agent.ID, task.ID)
	if err != nil {
		t.Fatalf("heartbeat inside the grace window must succeed: %v", err)
	}
	if heartbeat.Status != TaskStatusRunning {
		t.Fatalf("unexpected status: %s", heartbeat.Status)
	}
	service.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC) }
	if _, err := service.HeartbeatAgentTask(ctx, agent.ID, task.ID); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("heartbeat outside the grace window must fail: %v", err)
	}
}

func TestAgentIdentityIsWorkspaceScoped(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	source := agentSource(t, service, ctx)
	agent, key := agentIdentity(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	other := User{ID: uuid.NewString(), Email: "other-workspace@example.com", DisplayName: "other", Enabled: true, Revision: 1, CreatedAt: service.Now(), UpdatedAt: service.Now()}
	if err = service.Store.Update(context.Background(), func(tx Tx) error {
		return recordSave(tx, "learning_user", other.ID, userRecord{Profile: other})
	}); err != nil {
		t.Fatal(err)
	}
	otherCtx := WithWorkspace(context.Background(), other.ID)
	if _, err := service.CollectionTask(otherCtx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task must not leak across workspaces: %v", err)
	}
	if _, err := service.Agent(otherCtx, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agent must not leak across workspaces: %v", err)
	}
	if _, ok, err := service.ClaimAgentTask(otherCtx, agent.ID, 1); err != nil || ok {
		t.Fatalf("foreign workspace must not claim: ok=%v err=%v", ok, err)
	}
	if _, err := service.AgentForKey(context.Background(), key, PermissionTaskClaim); err != nil {
		t.Fatalf("key lookup is global by design: %v", err)
	}
}
