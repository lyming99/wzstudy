package study

import (
	"context"
	"testing"
)

func TestManualSourceAcceptsAgentIntegration(t *testing.T) {
	service := fixture(t, "memory")
	source, _, err := service.CreateSource(context.Background(), SourceInput{
		Slug: "agent-source", Name: "Agent 录入来源", Kind: "manual", Integration: "agent",
	})
	if err != nil {
		t.Fatalf("agent integration must be accepted: %v", err)
	}
	if source.Integration != "agent" {
		t.Fatalf("unexpected integration: %s", source.Integration)
	}
	if _, _, err := service.CreateSource(context.Background(), SourceInput{
		Slug: "unknown-source", Name: "Unknown", Kind: "manual", Integration: "webhook",
	}); err == nil {
		t.Fatal("unknown integration must still be rejected")
	}
}
