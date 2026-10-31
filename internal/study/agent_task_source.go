package study

import (
	"context"
	"errors"
)

const AgentCollectionSourceSlug = "agent-collect"

// EnsureAgentCollectionSource returns the workspace's automatic intake source
// used when a collection task is dispatched without choosing a source. It is
// created on first use and reused afterwards.
func (s *Service) EnsureAgentCollectionSource(ctx context.Context) (out Source, err error) {
	sources, err := s.Sources(ctx)
	if err != nil {
		return out, err
	}
	for _, source := range sources {
		if source.Slug == AgentCollectionSourceSlug && !source.System {
			return source, nil
		}
	}
	out, _, err = s.CreateSource(ctx, SourceInput{
		Slug:        AgentCollectionSourceSlug,
		Name:        "Agent 收集",
		Description: "派发收集任务时自动创建，用于接收采集结果",
		Kind:        "manual",
		Integration: "agent",
		Category:    "学习方法",
		Permissions: []string{PermissionIngest},
	})
	if errors.Is(err, ErrConflict) {
		sources, reloadErr := s.Sources(ctx)
		if reloadErr != nil {
			return Source{}, reloadErr
		}
		for _, source := range sources {
			if source.Slug == AgentCollectionSourceSlug && !source.System {
				return source, nil
			}
		}
	}
	return out, err
}
