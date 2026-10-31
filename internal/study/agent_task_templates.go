package study

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maximumTaskTemplateNameBytes = 80

// AgentTaskTemplate stores reusable collection-task settings for one
// administrator workspace. The source binding is intentionally excluded so a
// template can be applied to different sources without inheriting access.
type AgentTaskTemplate struct {
	OwnerID         string          `json:"owner_id,omitempty"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Title           string          `json:"title,omitempty"`
	Goal            string          `json:"goal,omitempty"`
	Constraints     TaskConstraints `json:"constraints"`
	Budget          TaskBudget      `json:"budget"`
	Priority        int             `json:"priority"`
	AssignedAgentID string          `json:"assigned_agent_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AgentTaskTemplateInput struct {
	Name            string          `json:"name"`
	Title           string          `json:"title"`
	Goal            string          `json:"goal"`
	Constraints     TaskConstraints `json:"constraints"`
	Budget          TaskBudget      `json:"budget"`
	Priority        int             `json:"priority"`
	AssignedAgentID string          `json:"assigned_agent_id"`
}

func normalizeAgentTaskTemplate(in AgentTaskTemplateInput, policy AgentPolicy) (AgentTaskTemplateInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Title = strings.TrimSpace(in.Title)
	in.Goal = strings.TrimSpace(in.Goal)
	in.AssignedAgentID = strings.TrimSpace(in.AssignedAgentID)
	if in.Name == "" || !validText(in.Name, maximumTaskTemplateNameBytes) {
		return in, ErrInvalid
	}
	if !validText(in.Goal, maximumTaskGoalBytes) || !validText(in.Title, maximumTaskTitleBytes) {
		return in, ErrInvalid
	}
	constraints, err := normalizeTaskConstraints(in.Constraints, policy)
	if err != nil {
		return in, err
	}
	in.Constraints = constraints
	if in.Budget.TimeoutSeconds == 0 && (constraints.ContentType == "video" || constraints.ContentType == "audio") {
		in.Budget.TimeoutSeconds = min(1800, policy.MaxTimeoutSeconds)
	}
	budget, err := normalizeTaskBudget(in.Budget, policy)
	if err != nil {
		return in, err
	}
	in.Budget = budget
	if in.Priority < -10 || in.Priority > 10 {
		return in, ErrInvalid
	}
	if in.AssignedAgentID != "" && uuid.Validate(in.AssignedAgentID) != nil {
		return in, ErrInvalid
	}
	return in, nil
}

func (s *Service) AgentTaskTemplates(ctx context.Context) (out []AgentTaskTemplate, err error) {
	out = []AgentTaskTemplate{}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[AgentTaskTemplate](tx, "agent_task_template")
		if e != nil {
			return e
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].CreatedAt.Equal(all[j].CreatedAt) {
				return all[i].Name < all[j].Name
			}
			return all[i].CreatedAt.After(all[j].CreatedAt)
		})
		out = all
		return nil
	})
	return
}

func (s *Service) CreateAgentTaskTemplate(ctx context.Context, in AgentTaskTemplateInput) (out AgentTaskTemplate, err error) {
	in, err = normalizeAgentTaskTemplate(in, s.agentPolicy())
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		all, e := recordsAs[AgentTaskTemplate](tx, "agent_task_template")
		if e != nil {
			return e
		}
		for _, other := range all {
			if strings.EqualFold(other.Name, in.Name) {
				return ErrConflict
			}
		}
		now := s.Now()
		out = AgentTaskTemplate{
			OwnerID: WorkspaceUserID(ctx), ID: uuid.NewString(), Name: in.Name,
			Title: in.Title, Goal: in.Goal, Constraints: in.Constraints, Budget: in.Budget,
			Priority: in.Priority, AssignedAgentID: in.AssignedAgentID,
			CreatedAt: now, UpdatedAt: now,
		}
		return recordSave(tx, "agent_task_template", out.ID, out)
	})
	return
}

func (s *Service) DeleteAgentTaskTemplate(ctx context.Context, id string) (out AgentTaskTemplate, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := recordAs[AgentTaskTemplate](tx, "agent_task_template", id)
		if e != nil {
			return e
		}
		out = v
		return tx.DeleteRecord("agent_task_template", id)
	})
	return
}
