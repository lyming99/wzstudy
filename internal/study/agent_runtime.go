package study

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// AgentRuntimeSettings binds the server-side agent runtime to a local working
// directory. It is stored per administrator workspace.
type AgentRuntimeSettings struct {
	InstallDir    string    `json:"install_dir,omitempty"`
	WorkDir       string    `json:"work_dir"`
	AutoStart     bool      `json:"auto_start"`
	ModelProvider string    `json:"model_provider,omitempty"`
	ModelBaseURL  string    `json:"model_base_url,omitempty"`
	ModelName     string    `json:"model_name,omitempty"`
	ModelAPIKey   string    `json:"model_api_key,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *Service) AgentRuntimeSettings(ctx context.Context) (out AgentRuntimeSettings, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		raw, e := tx.Record("agent_runtime", "settings")
		if errors.Is(e, ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		return json.Unmarshal(raw, &out)
	})
	return
}

func (s *Service) SaveAgentRuntimeSettings(ctx context.Context, in AgentRuntimeSettings) error {
	in.UpdatedAt = s.Now()
	return s.Store.Update(ctx, func(tx Tx) error {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		return tx.SaveRecord("agent_runtime", "settings", raw)
	})
}
