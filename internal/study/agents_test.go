package study

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDeleteAgentRequiresRevisionAndNoActiveTasks(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	agent, key := agentIdentity(t, service, ctx)

	if err := service.DeleteAgent(ctx, agent.ID, agent.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision must conflict: %v", err)
	}
	other := User{ID: uuid.NewString(), Email: "other-delete@example.com", DisplayName: "other", Enabled: true, Revision: 1, CreatedAt: service.Now(), UpdatedAt: service.Now()}
	if err := service.Store.Update(context.Background(), func(tx Tx) error {
		return recordSave(tx, "learning_user", other.ID, userRecord{Profile: other})
	}); err != nil {
		t.Fatalf("seed other workspace: %v", err)
	}
	if err := service.DeleteAgent(WithWorkspace(context.Background(), other.ID), agent.ID, agent.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace delete must be not found: %v", err)
	}

	source := agentSource(t, service, ctx)
	task, err := service.CreateCollectionTask(ctx, CollectionTaskInput{SourceID: source.ID, Goal: "collect", AgentID: agent.ID})
	if err != nil {
		t.Fatalf("create assigned task: %v", err)
	}
	if err := service.DeleteAgent(ctx, agent.ID, agent.Revision); !errors.Is(err, ErrAgentInUse) {
		t.Fatalf("active assigned task must block deletion: %v", err)
	}
	if _, err := service.CancelAgentTask(ctx, task.ID, "不再需要"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	if err := service.DeleteAgent(ctx, agent.ID, agent.Revision); err != nil {
		t.Fatalf("delete after task cancelled: %v", err)
	}
	if _, err := service.Agent(ctx, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted agent remains readable: %v", err)
	}
	if _, err := service.AgentForKey(context.Background(), key, PermissionTaskClaim); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted agent key still authenticates: %v", err)
	}
	if _, _, err := service.CreateAgent(ctx, AgentInput{Slug: agent.Slug, Name: "Replacement"}); err != nil {
		t.Fatalf("deleted slug must be reusable: %v", err)
	}
}

func TestAgentKeyBackfillsFromAuthenticatedClaim(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	agent, key, err := service.CreateAgent(ctx, AgentInput{Slug: "legacy-key", Name: "Legacy Key"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	// Simulate a record written before readable keys were retained.
	if err := service.Store.Update(ctx, func(tx Tx) error {
		raw, err := tx.Record("agent", agent.ID)
		if err != nil {
			return err
		}
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		internal, _ := record["internal"].(map[string]any)
		delete(internal, "key")
		raw, err = json.Marshal(record)
		if err != nil {
			return err
		}
		return tx.SaveRecord("agent", agent.ID, raw)
	}); err != nil {
		t.Fatalf("strip readable key: %v", err)
	}
	if visible, err := service.AgentSecret(ctx, agent.ID); err != nil || visible != "" {
		t.Fatalf("legacy secret = %q, %v", visible, err)
	}
	if err := service.TouchAgent(ctx, agent.ID, "1.0.0", "legacy-host", key); err != nil {
		t.Fatalf("touch legacy agent: %v", err)
	}
	if visible, err := service.AgentSecret(ctx, agent.ID); err != nil || visible != key {
		t.Fatalf("backfilled secret = %q, %v", visible, err)
	}
	if err := service.TouchAgent(ctx, agent.ID, "1.0.0", "legacy-host", "wzag_wrong"); err != nil {
		t.Fatalf("touch with wrong key: %v", err)
	}
	if visible, err := service.AgentSecret(ctx, agent.ID); err != nil || visible != key {
		t.Fatalf("wrong key changed secret = %q, %v", visible, err)
	}
}
