package study

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrTaskClaimed   = errors.New("task_claimed")
	ErrLeaseExpired  = errors.New("lease_expired")
	ErrQuota         = errors.New("item_quota_exceeded")
	ErrTaskCancelled = errors.New("task_cancelled")
	ErrDomain        = errors.New("domain_not_allowed")
	ErrContentType   = errors.New("content_type_mismatch")
	ErrAgentInUse    = errors.New("agent_in_use")
)

const (
	TaskStatusPending   = "pending"
	TaskStatusClaimed   = "claimed"
	TaskStatusRunning   = "running"
	TaskStatusSucceeded = "succeeded"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"
	TaskStatusExpired   = "expired"
)

const (
	maximumTaskItemsBatch   = 20
	maximumTaskEvents       = 200
	maximumTaskEventSeq     = 100000
	maximumTaskGoalBytes    = 4000
	maximumTaskTitleBytes   = 200
	maximumTaskSummaryBytes = 8000
	maximumTaskErrorBytes   = 2000
	defaultTaskRounds       = 100
	defaultTaskToolCalls    = 100
	maximumTaskRounds       = 1000
	maximumTaskToolCalls    = 1000
)

// AgentPolicy bounds collection task execution. Values are server-owned so an
// agent can never raise its own quota by editing a request.
type AgentPolicy struct {
	LeaseSeconds          int
	HeartbeatGraceSeconds int
	DefaultMaxItems       int
	MaxItems              int
	MaxTimeoutSeconds     int
	EventMaxBytes         int
	EventTotalBytes       int
}

func DefaultAgentPolicy() AgentPolicy {
	return AgentPolicy{
		LeaseSeconds:          300,
		HeartbeatGraceSeconds: 90,
		DefaultMaxItems:       50,
		MaxItems:              100,
		MaxTimeoutSeconds:     1800,
		EventMaxBytes:         8192,
		EventTotalBytes:       262144,
	}
}

func (s *Service) agentPolicy() AgentPolicy {
	p, d := s.AgentPolicy, DefaultAgentPolicy()
	if p.LeaseSeconds <= 0 {
		p.LeaseSeconds = d.LeaseSeconds
	}
	if p.HeartbeatGraceSeconds <= 0 {
		p.HeartbeatGraceSeconds = d.HeartbeatGraceSeconds
	}
	if p.DefaultMaxItems <= 0 {
		p.DefaultMaxItems = d.DefaultMaxItems
	}
	if p.MaxItems <= 0 {
		p.MaxItems = d.MaxItems
	}
	if p.DefaultMaxItems > p.MaxItems {
		p.DefaultMaxItems = p.MaxItems
	}
	if p.MaxTimeoutSeconds <= 0 {
		p.MaxTimeoutSeconds = d.MaxTimeoutSeconds
	}
	if p.EventMaxBytes <= 0 {
		p.EventMaxBytes = d.EventMaxBytes
	}
	if p.EventTotalBytes <= 0 {
		p.EventTotalBytes = d.EventTotalBytes
	}
	return p
}

type TaskConstraints struct {
	MaxItems        int      `json:"max_items"`
	DuplicatePolicy string   `json:"duplicate_policy,omitempty"`
	Language        string   `json:"language,omitempty"`
	Category        string   `json:"category,omitempty"`
	ContentType     string   `json:"content_type,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	AllowDomains    []string `json:"allow_domains,omitempty"`
}

type TaskBudget struct {
	MaxRounds      int `json:"max_rounds"`
	MaxToolCalls   int `json:"max_tool_calls"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

type TaskStats struct {
	ItemsCreated   int   `json:"items_created"`
	ItemsUpdated   int   `json:"items_updated"`
	ItemsUnchanged int   `json:"items_unchanged"`
	ItemsSkipped   int   `json:"items_skipped"`
	Rounds         int   `json:"rounds"`
	ToolCalls      int   `json:"tool_calls"`
	DurationMS     int64 `json:"duration_ms"`
}

type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type TaskResult struct {
	Status  string     `json:"status"`
	Summary string     `json:"summary,omitempty"`
	Error   *TaskError `json:"error,omitempty"`
}

// CollectionTask is a server-assigned collection job. The source binding is
// fixed at creation time; an agent can only push materials into that source.
type CollectionTask struct {
	OwnerID         string          `json:"owner_id,omitempty"`
	ID              string          `json:"id"`
	SourceID        string          `json:"source_id"`
	Title           string          `json:"title"`
	Goal            string          `json:"goal"`
	Constraints     TaskConstraints `json:"constraints"`
	Budget          TaskBudget      `json:"budget"`
	Status          string          `json:"status"`
	Priority        int             `json:"priority"`
	Attempts        int             `json:"attempts"`
	MaxAttempts     int             `json:"max_attempts"`
	AgentID         string          `json:"agent_id,omitempty"`
	AssignedAgentID string          `json:"assigned_agent_id,omitempty"`
	LeaseExpiresAt  *time.Time      `json:"lease_expires_at,omitempty"`
	Deadline        *time.Time      `json:"deadline,omitempty"`
	CancelRequested bool            `json:"cancel_requested"`
	CancelReason    string          `json:"cancel_reason,omitempty"`
	ReleaseReason   string          `json:"release_reason,omitempty"`
	Result          *TaskResult     `json:"result,omitempty"`
	Stats           TaskStats       `json:"stats"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

type CollectionTaskInput struct {
	SourceID    string          `json:"source_id"`
	Title       string          `json:"title"`
	Goal        string          `json:"goal"`
	Constraints TaskConstraints `json:"constraints"`
	Budget      TaskBudget      `json:"budget"`
	Priority    int             `json:"priority"`
	MaxAttempts int             `json:"max_attempts"`
	AgentID     string          `json:"assigned_agent_id"`
}

type AgentEvent struct {
	TaskID  string    `json:"task_id"`
	Attempt int       `json:"attempt"`
	Seq     int       `json:"seq"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text,omitempty"`
	Tool    string    `json:"tool,omitempty"`
	Summary string    `json:"summary,omitempty"`
	At      time.Time `json:"at"`
}

type AgentEventInput struct {
	Seq     int        `json:"seq"`
	Kind    string     `json:"kind"`
	Text    string     `json:"text,omitempty"`
	Tool    string     `json:"tool,omitempty"`
	Summary string     `json:"summary,omitempty"`
	At      *time.Time `json:"at,omitempty"`
}

var validAgentEventKinds = map[string]bool{
	"round_start": true,
	"assistant":   true,
	"tool_call":   true,
	"tool_result": true,
	"notice":      true,
	"finish":      true,
}

func normalizeTaskInput(in CollectionTaskInput, policy AgentPolicy) (CollectionTaskInput, error) {
	in.SourceID = strings.TrimSpace(in.SourceID)
	in.Goal = strings.TrimSpace(in.Goal)
	in.Title = strings.TrimSpace(in.Title)
	in.AgentID = strings.TrimSpace(in.AgentID)
	if in.Goal == "" || !validText(in.Goal, maximumTaskGoalBytes) {
		return in, ErrInvalid
	}
	if in.Title == "" {
		in.Title = truncateBytes(in.Goal, maximumTaskTitleBytes)
	}
	if !validText(in.Title, maximumTaskTitleBytes) {
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
	if in.MaxAttempts == 0 {
		in.MaxAttempts = 3
	}
	if in.MaxAttempts < 1 || in.MaxAttempts > 10 {
		return in, ErrInvalid
	}
	if in.AgentID != "" && uuid.Validate(in.AgentID) != nil {
		return in, ErrInvalid
	}
	return in, nil
}

// normalizeTaskConstraints is shared by one-off tasks and saved templates so a
// template can never store limits that task creation would reject.
func normalizeTaskConstraints(in TaskConstraints, policy AgentPolicy) (TaskConstraints, error) {
	if in.DuplicatePolicy == "" {
		in.DuplicatePolicy = "skip"
	}
	if in.DuplicatePolicy != "skip" && in.DuplicatePolicy != "update" {
		return in, ErrInvalid
	}
	if in.MaxItems == 0 {
		in.MaxItems = policy.DefaultMaxItems
	}
	if in.MaxItems < 1 || in.MaxItems > policy.MaxItems {
		return in, ErrInvalid
	}
	in.Language = strings.TrimSpace(in.Language)
	in.Category = strings.TrimSpace(in.Category)
	in.ContentType = strings.ToLower(strings.TrimSpace(in.ContentType))
	switch in.ContentType {
	case "", "article", "video", "audio", "carousel":
	default:
		return in, ErrInvalid
	}
	if !validText(in.Language, 35) || !validText(in.Category, 200) {
		return in, ErrInvalid
	}
	if len(in.Tags) > 20 {
		return in, ErrInvalid
	}
	tags := make([]string, 0, len(in.Tags))
	seen := map[string]bool{}
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !validText(tag, 80) {
			return in, ErrInvalid
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	sort.Strings(tags)
	in.Tags = tags
	if len(in.AllowDomains) > 20 {
		return in, ErrInvalid
	}
	domains := make([]string, 0, len(in.AllowDomains))
	seenDomain := map[string]bool{}
	for _, domain := range in.AllowDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, ".")
		if domain == "" || !validHostPattern(domain) {
			return in, ErrInvalid
		}
		if !seenDomain[domain] {
			domains = append(domains, domain)
			seenDomain[domain] = true
		}
	}
	sort.Strings(domains)
	in.AllowDomains = domains
	return in, nil
}

func normalizeTaskBudget(in TaskBudget, policy AgentPolicy) (TaskBudget, error) {
	if in.MaxRounds == 0 {
		in.MaxRounds = defaultTaskRounds
	}
	if in.MaxToolCalls == 0 {
		in.MaxToolCalls = defaultTaskToolCalls
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 300
	}
	if in.MaxRounds < 1 || in.MaxRounds > maximumTaskRounds || in.MaxToolCalls < 1 || in.MaxToolCalls > maximumTaskToolCalls ||
		in.TimeoutSeconds < 30 || in.TimeoutSeconds > policy.MaxTimeoutSeconds {
		return in, ErrInvalid
	}
	return in, nil
}

func validHostPattern(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if part == "" || len(part) > 63 {
			return false
		}
		for index, r := range part {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				continue
			}
			if r == '-' && index > 0 && index < len(part)-1 {
				continue
			}
			return false
		}
	}
	return true
}

func truncateBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	cut := value[:maximum]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut)
}

func (s *Service) CreateCollectionTask(ctx context.Context, in CollectionTaskInput) (out CollectionTask, err error) {
	policy := s.agentPolicy()
	in, err = normalizeTaskInput(in, policy)
	if err != nil {
		return
	}
	if in.SourceID == "" {
		source, e := s.EnsureAgentCollectionSource(ctx)
		if e != nil {
			return out, e
		}
		in.SourceID = source.ID
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		source, e := tx.Source(in.SourceID)
		if e != nil {
			return e
		}
		if !source.Enabled || !source.Allows(PermissionIngest) {
			return ErrDisabled
		}
		if in.AgentID != "" {
			agent, e := recordAs[agentRecord](tx, "agent", in.AgentID)
			if e != nil {
				return e
			}
			if !agent.Agent.Enabled || !agent.Agent.Allows(PermissionTaskClaim) {
				return ErrDisabled
			}
		}
		now := s.Now()
		out = CollectionTask{
			OwnerID: WorkspaceUserID(ctx), ID: uuid.NewString(), SourceID: in.SourceID,
			Title: in.Title, Goal: in.Goal, Constraints: in.Constraints, Budget: in.Budget,
			Status: TaskStatusPending, Priority: in.Priority, MaxAttempts: in.MaxAttempts,
			AssignedAgentID: in.AgentID, CreatedAt: now, UpdatedAt: now,
		}
		return recordSave(tx, "agent_task", out.ID, out)
	})
	return
}

func (s *Service) CollectionTask(ctx context.Context, id string) (out CollectionTask, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = recordAs[CollectionTask](tx, "agent_task", id); return e })
	return
}

func (s *Service) CollectionTasks(ctx context.Context, limit, offset int) (out []CollectionTask, total int, err error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return nil, 0, ErrInvalid
	}
	out = []CollectionTask{}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[CollectionTask](tx, "agent_task")
		if e != nil {
			return e
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].CreatedAt.Equal(all[j].CreatedAt) {
				return all[i].ID > all[j].ID
			}
			return all[i].CreatedAt.After(all[j].CreatedAt)
		})
		total = len(all)
		if offset < total {
			out = all[offset:min(offset+limit, total)]
		}
		return nil
	})
	return
}

// ClaimAgentTask leases one pending task to an agent inside a serialized write
// transaction. Expired leases are recycled first, so a crashed agent can never
// strand a task.
func (s *Service) ClaimAgentTask(ctx context.Context, agentID string, capacity int) (out CollectionTask, ok bool, err error) {
	if agentID == "" {
		return out, false, ErrUnauthorized
	}
	if capacity <= 0 {
		capacity = 1
	}
	if capacity > 4 {
		capacity = 4
	}
	policy := s.agentPolicy()
	now := s.Now()
	err = s.Store.Update(ctx, func(tx Tx) error {
		tasks, e := recordsAs[CollectionTask](tx, "agent_task")
		if e != nil {
			return e
		}
		for index := range tasks {
			task := tasks[index]
			if task.Status != TaskStatusClaimed && task.Status != TaskStatusRunning {
				continue
			}
			if task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(now) {
				continue
			}
			task = requeueTask(task, now)
			if e := recordSave(tx, "agent_task", task.ID, task); e != nil {
				return e
			}
			tasks[index] = task
		}
		best := -1
		for index, task := range tasks {
			if task.Status != TaskStatusPending || task.CancelRequested {
				continue
			}
			if task.AssignedAgentID != "" && task.AssignedAgentID != agentID {
				continue
			}
			if best < 0 || taskPrecedes(task, tasks[best]) {
				best = index
			}
		}
		if best < 0 {
			return nil
		}
		task := tasks[best]
		lease := now.Add(time.Duration(policy.LeaseSeconds) * time.Second)
		deadline := now.Add(time.Duration(task.Budget.TimeoutSeconds) * time.Second)
		task.Status = TaskStatusClaimed
		task.AgentID = agentID
		task.Attempts++
		task.LeaseExpiresAt = &lease
		task.Deadline = &deadline
		task.ReleaseReason = ""
		task.UpdatedAt = now
		if e := recordSave(tx, "agent_task", task.ID, task); e != nil {
			return e
		}
		out, ok = task, true
		return nil
	})
	return
}

func taskPrecedes(left, right CollectionTask) bool {
	if left.Priority != right.Priority {
		return left.Priority > right.Priority
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.ID < right.ID
}

func requeueTask(task CollectionTask, now time.Time) CollectionTask {
	task.LeaseExpiresAt = nil
	task.UpdatedAt = now
	if task.CancelRequested {
		task.Status = TaskStatusCancelled
		task.FinishedAt = &now
		task.Result = &TaskResult{Status: TaskStatusCancelled, Summary: "任务在租约到期时已被取消"}
		return task
	}
	if task.Attempts >= task.MaxAttempts {
		task.Status = TaskStatusExpired
		task.FinishedAt = &now
		task.Result = &TaskResult{Status: TaskStatusExpired, Error: &TaskError{Code: "attempts_exhausted", Message: "超过最大执行次数"}}
		return task
	}
	task.Status = TaskStatusPending
	task.AgentID = ""
	return task
}

// leaseValid allows one heartbeat grace window beyond the lease so a slightly
// late heartbeat does not lose a task to clock skew or a short network stall.
func (s *Service) leaseValid(task CollectionTask, now time.Time) bool {
	if task.LeaseExpiresAt == nil {
		return false
	}
	grace := time.Duration(s.agentPolicy().HeartbeatGraceSeconds) * time.Second
	return task.LeaseExpiresAt.Add(grace).After(now)
}

// HeartbeatAgentTask renews the lease and reports cancellation. An expired or
// foreign lease fails closed so the agent stops writing.
func (s *Service) HeartbeatAgentTask(ctx context.Context, agentID, taskID string) (out CollectionTask, err error) {
	policy := s.agentPolicy()
	err = s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		if task.AgentID != agentID {
			return ErrTaskClaimed
		}
		if task.Status != TaskStatusClaimed && task.Status != TaskStatusRunning {
			return ErrConflict
		}
		now := s.Now()
		if !s.leaseValid(task, now) {
			return ErrLeaseExpired
		}
		lease := now.Add(time.Duration(policy.LeaseSeconds) * time.Second)
		task.LeaseExpiresAt = &lease
		task.Status = TaskStatusRunning
		task.UpdatedAt = now
		out = task
		return recordSave(tx, "agent_task", task.ID, task)
	})
	return
}

func (s *Service) AppendAgentEvents(ctx context.Context, agentID, taskID string, attempt int, events []AgentEventInput) (accepted int, highest int, err error) {
	if len(events) == 0 || len(events) > maximumTaskEvents {
		return 0, 0, ErrInvalid
	}
	policy := s.agentPolicy()
	err = s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		if task.AgentID != agentID {
			return ErrTaskClaimed
		}
		if task.Attempts != attempt {
			return ErrLeaseExpired
		}
		if task.Status != TaskStatusClaimed && task.Status != TaskStatusRunning {
			return ErrConflict
		}
		now := s.Now()
		existing, e := recordsAs[AgentEvent](tx, "agent_event")
		if e != nil {
			return e
		}
		used := 0
		for _, event := range existing {
			if event.TaskID == taskID && event.Attempt == attempt {
				used += agentEventBytes(event.Text, event.Tool, event.Summary)
				if event.Seq > highest {
					highest = event.Seq
				}
			}
		}
		for _, in := range events {
			if in.Seq < 1 || in.Seq > maximumTaskEventSeq || !validAgentEventKinds[in.Kind] {
				return ErrInvalid
			}
			if !validText(in.Text, policy.EventMaxBytes) || !validText(in.Summary, policy.EventMaxBytes) || !validText(in.Tool, 64) {
				return ErrInvalid
			}
			used += agentEventBytes(in.Text, in.Tool, in.Summary)
			if used > policy.EventTotalBytes {
				return ErrQuota
			}
			at := now
			if in.At != nil {
				at = in.At.UTC()
			}
			event := AgentEvent{TaskID: taskID, Attempt: attempt, Seq: in.Seq, Kind: in.Kind, Text: in.Text, Tool: in.Tool, Summary: in.Summary, At: at}
			if e := recordSave(tx, "agent_event", agentEventID(taskID, attempt, in.Seq), event); e != nil {
				return e
			}
			if in.Seq > highest {
				highest = in.Seq
			}
			accepted++
		}
		task.UpdatedAt = now
		return recordSave(tx, "agent_task", task.ID, task)
	})
	return
}

func agentEventBytes(text, tool, summary string) int {
	return len(text) + len(tool) + len(summary) + 64
}

func agentEventID(taskID string, attempt, seq int) string {
	return fmt.Sprintf("%s:%d:%06d", taskID, attempt, seq)
}

func (s *Service) AgentTaskEvents(ctx context.Context, taskID string, limit int) (out []AgentEvent, err error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	out = []AgentEvent{}
	err = s.Store.View(ctx, func(tx Tx) error {
		if _, e := recordAs[CollectionTask](tx, "agent_task", taskID); e != nil {
			return e
		}
		all, e := recordsAs[AgentEvent](tx, "agent_event")
		if e != nil {
			return e
		}
		for _, event := range all {
			if event.TaskID == taskID {
				out = append(out, event)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Attempt != out[j].Attempt {
				return out[i].Attempt < out[j].Attempt
			}
			return out[i].Seq < out[j].Seq
		})
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
		return nil
	})
	return
}

// IngestAgentTaskItems binds pushed materials to the task's source. The URL
// host allowlist and the item quota are enforced server-side.
func (s *Service) IngestAgentTaskItems(ctx context.Context, agentID, taskID string, attempt int, items []Content) (out IntakeReceipt, err error) {
	if len(items) == 0 || len(items) > maximumTaskItemsBatch {
		return out, ErrInvalid
	}
	policy := s.agentPolicy()
	err = s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		if task.AgentID != agentID {
			return ErrTaskClaimed
		}
		if task.Attempts != attempt {
			return ErrLeaseExpired
		}
		if task.Status != TaskStatusClaimed && task.Status != TaskStatusRunning {
			return ErrConflict
		}
		now := s.Now()
		if !s.leaseValid(task, now) {
			return ErrLeaseExpired
		}
		if task.CancelRequested {
			return ErrTaskCancelled
		}
		source, e := tx.Source(task.SourceID)
		if e != nil {
			return e
		}
		if !source.Enabled || !source.Allows(PermissionIngest) {
			return ErrDisabled
		}
		used := task.Stats.ItemsCreated + task.Stats.ItemsUpdated + task.Stats.ItemsUnchanged
		remaining := task.Constraints.MaxItems - used
		if remaining <= 0 {
			return ErrQuota
		}
		if e := validateTaskDomains(task.Constraints.AllowDomains, items); e != nil {
			return e
		}
		skipped := 0
		if task.Constraints.DuplicatePolicy != "update" {
			items, skipped, e = filterCollectionDuplicates(tx, task.SourceID, items)
			if e != nil {
				return e
			}
		}
		if len(items) > remaining {
			return ErrQuota
		}
		// Resolve uploaded assets from the registry before checking the task's
		// content type. The client may submit only asset IDs; MIME and size are
		// server-owned and must come from the registered upload, not the agent.
		for index := range items {
			for n, asset := range items[index].Assets {
				if stored, e := recordAs[Asset](tx, "asset", asset.ID); e == nil {
					items[index].Assets[n] = stored
				}
			}
		}
		if e := validateTaskContentType(task.Constraints.ContentType, items); e != nil {
			return e
		}
		var result IngestResult
		var receipt IntakeReceipt
		if len(items) == 0 {
			receipt, e = s.recordIntake(tx, source, result, "agent", task.ID)
		} else {
			result, e = s.ingest(tx, source, items, "agent", task.ID)
			if e == nil {
				receipt, e = recordAs[IntakeReceipt](tx, "intake", result.ReceiptID)
			}
		}
		if e != nil {
			return e
		}
		receipt.Skipped = skipped
		e = recordSave(tx, "intake", receipt.ID, receipt)
		if e != nil {
			return e
		}
		task.Stats.ItemsCreated += result.Created
		task.Stats.ItemsUpdated += result.Updated
		task.Stats.ItemsUnchanged += result.Unchanged
		task.Stats.ItemsSkipped += skipped
		task.UpdatedAt = now
		lease := now.Add(time.Duration(policy.LeaseSeconds) * time.Second)
		task.LeaseExpiresAt = &lease
		if e := recordSave(tx, "agent_task", task.ID, task); e != nil {
			return e
		}
		out = receipt
		return nil
	})
	return
}

func validateTaskDomains(allow []string, items []Content) error {
	if len(allow) == 0 {
		return nil
	}
	for _, item := range items {
		if item.URL == "" {
			return ErrDomain
		}
		parsed, err := url.Parse(item.URL)
		if err != nil || parsed.Hostname() == "" {
			return ErrDomain
		}
		host := strings.ToLower(parsed.Hostname())
		matched := false
		for _, domain := range allow {
			domain = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(domain, ".")))
			if domain == "" {
				continue
			}
			if host == domain || strings.HasSuffix(host, "."+domain) {
				matched = true
				break
			}
		}
		if !matched {
			return ErrDomain
		}
	}
	return nil
}

// CompleteAgentTask is idempotent: repeating a terminal report returns success
// and never rewrites the stored result.
func (s *Service) CompleteAgentTask(ctx context.Context, agentID, taskID string, attempt int, status, summary string, stats TaskStats, taskErr *TaskError) error {
	if status != TaskStatusSucceeded && status != TaskStatusFailed && status != TaskStatusCancelled {
		return ErrInvalid
	}
	summary = strings.TrimSpace(summary)
	if !validText(summary, maximumTaskSummaryBytes) {
		return ErrInvalid
	}
	if taskErr != nil {
		taskErr.Code = strings.TrimSpace(taskErr.Code)
		taskErr.Message = strings.TrimSpace(taskErr.Message)
		if taskErr.Code == "" || !validText(taskErr.Code, 64) || !validText(taskErr.Message, maximumTaskErrorBytes) {
			return ErrInvalid
		}
	}
	if stats.Rounds < 0 || stats.ToolCalls < 0 || stats.ItemsCreated < 0 || stats.ItemsUpdated < 0 || stats.ItemsUnchanged < 0 || stats.DurationMS < 0 {
		return ErrInvalid
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		if task.Status == TaskStatusSucceeded || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled || task.Status == TaskStatusExpired {
			return nil
		}
		if task.AgentID != agentID {
			return ErrTaskClaimed
		}
		if task.Attempts != attempt {
			return ErrLeaseExpired
		}
		now := s.Now()
		task.Status = status
		task.Result = &TaskResult{Status: status, Summary: summary, Error: taskErr}
		task.Stats.Rounds = stats.Rounds
		task.Stats.ToolCalls = stats.ToolCalls
		task.Stats.DurationMS = stats.DurationMS
		task.LeaseExpiresAt = nil
		task.UpdatedAt = now
		task.FinishedAt = &now
		return recordSave(tx, "agent_task", task.ID, task)
	})
}

// ReleaseAgentTask returns a lease without recording success, used on graceful
// shutdown or when the agent cannot make progress locally.
func (s *Service) ReleaseAgentTask(ctx context.Context, agentID, taskID string, attempt int, reason string) error {
	reason = strings.TrimSpace(reason)
	if !validText(reason, 500) {
		return ErrInvalid
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		if task.AgentID != agentID || task.Attempts != attempt {
			return ErrTaskClaimed
		}
		if task.Status != TaskStatusClaimed && task.Status != TaskStatusRunning {
			return nil
		}
		task.ReleaseReason = reason
		task = requeueTask(task, s.Now())
		return recordSave(tx, "agent_task", task.ID, task)
	})
}

func (s *Service) CancelAgentTask(ctx context.Context, taskID, reason string) (out CollectionTask, err error) {
	reason = strings.TrimSpace(reason)
	if !validText(reason, 500) {
		return out, ErrInvalid
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		now := s.Now()
		if task.Status == TaskStatusSucceeded || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled || task.Status == TaskStatusExpired {
			out = task
			return nil
		}
		if task.Status == TaskStatusPending {
			task.Status = TaskStatusCancelled
			task.FinishedAt = &now
			task.Result = &TaskResult{Status: TaskStatusCancelled, Summary: reason}
		} else {
			task.CancelRequested = true
			task.CancelReason = reason
		}
		task.UpdatedAt = now
		out = task
		return recordSave(tx, "agent_task", task.ID, task)
	})
	return
}

func (s *Service) RetryAgentTask(ctx context.Context, taskID string) (out CollectionTask, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		switch task.Status {
		case TaskStatusFailed, TaskStatusExpired, TaskStatusCancelled:
		case TaskStatusPending:
			out = task
			return nil
		default:
			return ErrConflict
		}
		task.Status = TaskStatusPending
		task.AgentID = ""
		task.Attempts = 0
		task.LeaseExpiresAt = nil
		task.Deadline = nil
		task.CancelRequested = false
		task.CancelReason = ""
		task.ReleaseReason = ""
		task.Result = nil
		task.Stats = TaskStats{}
		task.FinishedAt = nil
		task.UpdatedAt = s.Now()
		out = task
		return recordSave(tx, "agent_task", task.ID, task)
	})
	return
}

type AgentTaskSummaryCounts struct {
	Pending   int `json:"pending"`
	Claimed   int `json:"claimed"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Expired   int `json:"expired"`
	Total     int `json:"total"`
}

func (s *Service) AgentTaskSummary(ctx context.Context) (out AgentTaskSummaryCounts, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		tasks, e := recordsAs[CollectionTask](tx, "agent_task")
		if e != nil {
			return e
		}
		for _, task := range tasks {
			out.Total++
			switch task.Status {
			case TaskStatusPending:
				out.Pending++
			case TaskStatusClaimed:
				out.Claimed++
			case TaskStatusRunning:
				out.Running++
			case TaskStatusSucceeded:
				out.Succeeded++
			case TaskStatusFailed:
				out.Failed++
			case TaskStatusCancelled:
				out.Cancelled++
			case TaskStatusExpired:
				out.Expired++
			}
		}
		return nil
	})
	return
}
