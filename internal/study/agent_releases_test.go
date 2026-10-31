package study

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func releaseFixture() AgentReleaseInput {
	return AgentReleaseInput{
		Version: "0.1.0", Platform: "windows", Arch: "amd64",
		Filename: "wzstudy-agent.exe", Size: 1024,
		SHA256: strings.Repeat("a", 64), Notes: "首个版本",
	}
}

func TestAgentReleaseLifecycle(t *testing.T) {
	service := fixture(t, "memory")
	input := releaseFixture()
	release, err := service.CreateAgentRelease(context.Background(), input)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	if release.Version != "0.1.0" || release.Platform != "windows" || release.ID == "" {
		t.Fatalf("unexpected release: %+v", release)
	}
	if _, err := service.CreateAgentRelease(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate version must conflict: %v", err)
	}
	another := input
	another.Arch = "arm64"
	if _, err := service.CreateAgentRelease(context.Background(), another); err != nil {
		t.Fatalf("different architecture must be allowed: %v", err)
	}
	releases, err := service.AgentReleases(context.Background())
	if err != nil || len(releases) != 2 {
		t.Fatalf("list releases: %v %+v", err, releases)
	}
	if _, err := service.DeleteAgentRelease(context.Background(), release.ID); err != nil {
		t.Fatalf("delete release: %v", err)
	}
	if _, err := service.AgentRelease(context.Background(), release.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted release must be gone: %v", err)
	}
}

func TestAgentReleaseValidation(t *testing.T) {
	service := fixture(t, "memory")
	cases := []func(*AgentReleaseInput){
		func(in *AgentReleaseInput) { in.Version = "latest" },
		func(in *AgentReleaseInput) { in.Platform = "android" },
		func(in *AgentReleaseInput) { in.Arch = "386" },
		func(in *AgentReleaseInput) { in.Filename = "payload.sh" },
		func(in *AgentReleaseInput) { in.Size = 0 },
		func(in *AgentReleaseInput) { in.SHA256 = "not-a-hash" },
		func(in *AgentReleaseInput) { in.Notes = strings.Repeat("x", maximumReleaseNotes+1) },
	}
	for index, mutate := range cases {
		input := releaseFixture()
		mutate(&input)
		if _, err := service.CreateAgentRelease(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d must be rejected, got %v", index, err)
		}
	}
	// Hidden traversal only strips the directory; the stored name stays safe.
	input := releaseFixture()
	input.Filename = "../payload.exe"
	release, err := service.CreateAgentRelease(context.Background(), input)
	if err != nil || release.Filename != "payload.exe" {
		t.Fatalf("filename must be reduced to a base name: %+v %v", release, err)
	}
}
