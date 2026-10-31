package study

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAgentTaskTemplateRoundTrip(t *testing.T) {
	service, ctx, owner := agentFixture(t)
	created, err := service.CreateAgentTaskTemplate(ctx, AgentTaskTemplateInput{
		Name:  "官方博客文章",
		Title: "Go 官方博客",
		Goal:  "采集 Go 官方博客最近三篇文章，整理成中文学习资料",
		Constraints: TaskConstraints{
			MaxItems:     3,
			ContentType:  "ARTICLE",
			Language:     " zh-CN ",
			Category:     "编程",
			Tags:         []string{"后端", "Go", "go", "Go"},
			AllowDomains: []string{"Go.dev", ".blog.golang.org", "go.dev"},
		},
		Budget:   TaskBudget{MaxRounds: 12, MaxToolCalls: 20, TimeoutSeconds: 240},
		Priority: 2,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if created.ID == "" || created.OwnerID != owner || created.Name != "官方博客文章" {
		t.Fatalf("unexpected template: %+v", created)
	}
	if created.Constraints.ContentType != "article" || created.Constraints.Language != "zh-CN" || created.Constraints.MaxItems != 3 {
		t.Fatalf("constraints were not normalized: %+v", created.Constraints)
	}
	if got, want := strings.Join(created.Constraints.Tags, ","), "Go,go,后端"; got != want {
		t.Fatalf("tags = %q, want %q", got, want)
	}
	if got, want := strings.Join(created.Constraints.AllowDomains, ","), "blog.golang.org,go.dev"; got != want {
		t.Fatalf("domains = %q, want %q", got, want)
	}
	if created.Budget != (TaskBudget{MaxRounds: 12, MaxToolCalls: 20, TimeoutSeconds: 240}) || created.Priority != 2 {
		t.Fatalf("budget/priority were not kept: %+v", created)
	}

	list, err := service.AgentTaskTemplates(ctx)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list templates: %v %+v", err, list)
	}

	if _, err := service.CreateAgentTaskTemplate(ctx, AgentTaskTemplateInput{Name: "官方博客文章"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name must conflict: %v", err)
	}
	if _, err := service.CreateAgentTaskTemplate(ctx, AgentTaskTemplateInput{Name: "Daily Digest"}); err != nil {
		t.Fatalf("create second template: %v", err)
	}
	if _, err := service.CreateAgentTaskTemplate(ctx, AgentTaskTemplateInput{Name: "daily digest"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name must ignore case: %v", err)
	}

	deleted, err := service.DeleteAgentTaskTemplate(ctx, created.ID)
	if err != nil || deleted.ID != created.ID {
		t.Fatalf("delete template: %v %+v", err, deleted)
	}
	list, err = service.AgentTaskTemplates(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Daily Digest" {
		t.Fatalf("templates after delete: %v %+v", err, list)
	}
	if _, err := service.DeleteAgentTaskTemplate(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice must be missing: %v", err)
	}
}

func TestAgentTaskTemplateDefaultsAndValidation(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	created, err := service.CreateAgentTaskTemplate(ctx, AgentTaskTemplateInput{Name: "极简模板"})
	if err != nil {
		t.Fatalf("create minimal template: %v", err)
	}
	if created.Goal != "" || created.Title != "" {
		t.Fatalf("empty goal/title must stay empty: %+v", created)
	}
	if created.Constraints.MaxItems != service.agentPolicy().DefaultMaxItems {
		t.Fatalf("default max items = %d", created.Constraints.MaxItems)
	}
	if created.Budget != (TaskBudget{MaxRounds: defaultTaskRounds, MaxToolCalls: defaultTaskToolCalls, TimeoutSeconds: 300}) {
		t.Fatalf("default budget = %+v", created.Budget)
	}

	cases := map[string]AgentTaskTemplateInput{
		"missing name":       {},
		"name too long":      {Name: strings.Repeat("模", 27)},
		"goal too long":      {Name: "长目标", Goal: strings.Repeat("a", maximumTaskGoalBytes+1)},
		"quota too high":     {Name: "配额", Constraints: TaskConstraints{MaxItems: 101}},
		"unknown type":       {Name: "类型", Constraints: TaskConstraints{ContentType: "pdf"}},
		"empty tag":          {Name: "标签", Constraints: TaskConstraints{Tags: []string{" "}}},
		"invalid domain":     {Name: "域名", Constraints: TaskConstraints{AllowDomains: []string{"https://go.dev"}}},
		"rounds too low":     {Name: "轮次", Budget: TaskBudget{MaxRounds: 1, MaxToolCalls: 1, TimeoutSeconds: 29}},
		"priority too high":  {Name: "优先级", Priority: 11},
		"invalid agent uuid": {Name: "采集端", AssignedAgentID: "collector"},
	}
	for label, in := range cases {
		if _, err := service.CreateAgentTaskTemplate(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: expected ErrInvalid, got %v", label, err)
		}
	}
}

func TestAgentTaskTemplatesAreWorkspaceScoped(t *testing.T) {
	service, ctx, _ := agentFixture(t)
	created, err := service.CreateAgentTaskTemplate(ctx, AgentTaskTemplateInput{Name: "私有模板", Goal: "只属于当前管理员"})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	other := User{ID: uuid.NewString(), Email: "other-template@example.com", DisplayName: "other", Enabled: true, Revision: 1, CreatedAt: ref, UpdatedAt: ref}
	if err := service.Store.Update(context.Background(), func(tx Tx) error {
		return recordSave(tx, "learning_user", other.ID, userRecord{Profile: other})
	}); err != nil {
		t.Fatal(err)
	}
	otherCtx := WithWorkspace(context.Background(), other.ID)
	list, err := service.AgentTaskTemplates(otherCtx)
	if err != nil || len(list) != 0 {
		t.Fatalf("other workspace must not see templates: %v %+v", err, list)
	}
	if _, err := service.DeleteAgentTaskTemplate(otherCtx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace must not delete templates: %v", err)
	}
	list, err = service.AgentTaskTemplates(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("owner templates must remain: %v %+v", err, list)
	}
}
