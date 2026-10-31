package study

import (
	"context"
	"testing"
)

func TestAgentRuntimeSettingsPersist(t *testing.T) {
	service := fixture(t, "memory")
	empty, err := service.AgentRuntimeSettings(context.Background())
	if err != nil || empty.WorkDir != "" {
		t.Fatalf("unconfigured runtime: %+v %v", empty, err)
	}
	want := AgentRuntimeSettings{WorkDir: `D:\app\wzstudy-agent`, AutoStart: true}
	if err := service.SaveAgentRuntimeSettings(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.AgentRuntimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkDir != want.WorkDir || !loaded.AutoStart || loaded.UpdatedAt.IsZero() {
		t.Fatalf("unexpected settings: %+v", loaded)
	}
}
