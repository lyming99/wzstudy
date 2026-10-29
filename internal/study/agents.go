package study

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// PermissionTaskClaim allows an agent to claim collection tasks. It never
	// grants material reading; task-scoped ingestion is checked separately.
	PermissionTaskClaim = "task.claim"
)

var agentPermissions = []string{PermissionTaskClaim, PermissionIngest}

// Agent is a collection worker identity. Agents execute server-assigned tasks
// and may only ingest through the source bound to the task they hold; they can
// never query the material library or manage sources.
type Agent struct {
	OwnerID     string     `json:"owner_id,omitempty"`
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Enabled     bool       `json:"enabled"`
	Permissions []string   `json:"permissions"`
	Revision    int64      `json:"revision"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	Version     string     `json:"version,omitempty"`
	Hostname    string     `json:"hostname,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type AgentInput struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled,omitempty"`
	Permissions []string `json:"permissions"`
}

type agentRecord struct {
	Agent  Agent `json:"agent"`
	Secret struct {
		KeyHash string `json:"key_hash"`
	} `json:"internal"`
}

func normalizeAgent(in AgentInput) (AgentInput, error) {
	in.Slug, in.Name, in.Description = strings.TrimSpace(in.Slug), strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if !slugPattern.MatchString(in.Slug) || in.Name == "" || !validText(in.Name, 200) || !validText(in.Description, 4000) {
		return in, ErrInvalid
	}
	var err error
	in.Permissions, err = normalizePolicy(in.Permissions, agentPermissions)
	return in, err
}

func cloneAgent(v Agent) Agent {
	v.Permissions = slices.Clone(v.Permissions)
	return v
}

func (v Agent) Allows(permission string) bool {
	return slices.Contains(v.Permissions, permission)
}

func newAgentKey() (string, error) {
	key, err := NewKey()
	return strings.Replace(key, "wzsk_", "wzag_", 1), err
}

func saveAgent(tx Tx, v Agent, hash string) error {
	records, err := recordsAs[agentRecord](tx, "agent")
	if err != nil {
		return err
	}
	for _, other := range records {
		if other.Agent.ID != v.ID && (other.Agent.Slug == v.Slug || (hash != "" && other.Secret.KeyHash == hash)) {
			return ErrConflict
		}
	}
	r := agentRecord{Agent: cloneAgent(v)}
	r.Secret.KeyHash = hash
	return recordSave(tx, "agent", v.ID, r)
}

func (s *Service) Agents(ctx context.Context) (out []Agent, err error) {
	out = []Agent{}
	err = s.Store.View(ctx, func(tx Tx) error {
		records, e := recordsAs[agentRecord](tx, "agent")
		if e != nil {
			return e
		}
		for _, r := range records {
			out = append(out, cloneAgent(r.Agent))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
		return nil
	})
	return
}

func (s *Service) Agent(ctx context.Context, id string) (out Agent, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		r, e := recordAs[agentRecord](tx, "agent", id)
		out = cloneAgent(r.Agent)
		return e
	})
	return
}

func (s *Service) CreateAgent(ctx context.Context, in AgentInput) (out Agent, key string, err error) {
	in, err = normalizeAgent(in)
	if err != nil {
		return
	}
	key, err = newAgentKey()
	if err != nil {
		return
	}
	permissions := in.Permissions
	if permissions == nil {
		permissions = []string{PermissionTaskClaim, PermissionIngest}
	}
	enabled := in.Enabled == nil || *in.Enabled
	now := s.Now()
	out = Agent{OwnerID: WorkspaceUserID(ctx), ID: uuid.NewString(), Slug: in.Slug, Name: in.Name, Description: in.Description, Enabled: enabled, Permissions: slices.Clone(permissions), Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.Store.Update(ctx, func(tx Tx) error { return saveAgent(tx, out, HashKey(key)) })
	if err != nil {
		key = ""
	}
	return
}

func (s *Service) UpdateAgent(ctx context.Context, id string, revision int64, in AgentInput) (out Agent, err error) {
	in, err = normalizeAgent(in)
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		r, e := recordAs[agentRecord](tx, "agent", id)
		if e != nil {
			return e
		}
		v := r.Agent
		if v.Revision != revision {
			return ErrConflict
		}
		if v.Slug != in.Slug {
			return ErrInvalid
		}
		v.Name, v.Description = in.Name, in.Description
		if in.Enabled != nil {
			v.Enabled = *in.Enabled
		}
		if in.Permissions != nil {
			v.Permissions = slices.Clone(in.Permissions)
		}
		v.Revision++
		v.UpdatedAt = s.Now()
		out = cloneAgent(v)
		return saveAgent(tx, v, r.Secret.KeyHash)
	})
	return
}

func (s *Service) RotateAgentKey(ctx context.Context, id string, revision int64) (out Agent, key string, err error) {
	key, err = newAgentKey()
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		r, e := recordAs[agentRecord](tx, "agent", id)
		if e != nil {
			return e
		}
		if r.Agent.Revision != revision {
			return ErrConflict
		}
		r.Agent.Revision++
		r.Agent.UpdatedAt = s.Now()
		out = cloneAgent(r.Agent)
		return saveAgent(tx, r.Agent, HashKey(key))
	})
	if err != nil {
		key = ""
	}
	return
}

// TouchAgent records liveness without bumping the revision, so a high-frequency
// heartbeat can never invalidate an administrator's edit. Writes are throttled.
func (s *Service) TouchAgent(ctx context.Context, id, version, hostname string) error {
	version, hostname = strings.TrimSpace(version), strings.TrimSpace(hostname)
	if len(version) > 64 || len(hostname) > 200 || strings.ContainsAny(version+hostname, "\r\n\x00") {
		version, hostname = "", ""
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		r, err := recordAs[agentRecord](tx, "agent", id)
		if err != nil {
			return err
		}
		now := s.Now()
		if r.Agent.LastSeenAt != nil && now.Sub(*r.Agent.LastSeenAt) < 30*time.Second && r.Agent.Version == version && r.Agent.Hostname == hostname {
			return nil
		}
		r.Agent.LastSeenAt = &now
		if version != "" {
			r.Agent.Version = version
		}
		if hostname != "" {
			r.Agent.Hostname = hostname
		}
		return saveAgent(tx, r.Agent, r.Secret.KeyHash)
	})
}

func agentAccess(tx Tx, key, permission string) (Agent, error) {
	if key == "" {
		return Agent{}, ErrUnauthorized
	}
	records, err := recordsAs[agentRecord](tx, "agent")
	if err != nil {
		return Agent{}, err
	}
	for _, record := range records {
		if !EqualKey(record.Secret.KeyHash, key) {
			continue
		}
		if !record.Agent.Enabled {
			return Agent{}, ErrDisabled
		}
		if !record.Agent.Allows(permission) {
			return Agent{}, ErrForbidden
		}
		return cloneAgent(record.Agent), nil
	}
	return Agent{}, ErrUnauthorized
}

func (s *Service) AgentForKey(ctx context.Context, key, permission string) (out Agent, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = agentAccess(tx, key, permission); return e })
	return
}
