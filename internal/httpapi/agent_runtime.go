package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"wzstudy/internal/agentruntime"
)

func (s *Server) agentRuntimeOrFail(w http.ResponseWriter) (*agentruntime.Manager, bool) {
	if s.runtime == nil {
		failure(w, http.StatusServiceUnavailable, "agent_runtime_unavailable", "服务端未启用采集端运行时管理")
		return nil, false
	}
	return s.runtime, true
}

func (s *Server) adminAgentRuntime(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.agentRuntimeOrFail(w)
	if !ok {
		return
	}
	success(w, 200, manager.Status())
}

func (s *Server) adminUpdateAgentRuntime(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.agentRuntimeOrFail(w)
	if !ok {
		return
	}
	var in struct {
		InstallDir string `json:"install_dir"`
		WorkDir    string `json:"work_dir"`
		AutoStart  bool   `json:"auto_start"`
		Restart    bool   `json:"restart"`
		Model      struct {
			Provider    string `json:"provider"`
			BaseURL     string `json:"base_url"`
			Model       string `json:"model"`
			APIKey      string `json:"api_key"`
			ClearAPIKey bool   `json:"clear_api_key"`
		} `json:"model"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := manager.UpdateSettings(r.Context(), in.InstallDir, in.WorkDir, in.AutoStart, agentruntime.ModelSettings{
		Provider:    in.Model.Provider,
		BaseURL:     in.Model.BaseURL,
		Model:       in.Model.Model,
		APIKey:      in.Model.APIKey,
		ClearAPIKey: in.Model.ClearAPIKey,
	}); err != nil {
		switch {
		case errors.Is(err, agentruntime.ErrInvalidInstall):
			failure(w, 400, "invalid_install_dir", "程序目录必须存在，且包含 wzstudy-agent.exe 与 agent.env")
		case errors.Is(err, agentruntime.ErrInvalidWorkDir):
			failure(w, 400, "invalid_work_dir", "工作路径必须是绝对路径")
		case errors.Is(err, agentruntime.ErrInvalidModel):
			failure(w, 400, "invalid_model", "模型配置无效：请检查供应商、地址、模型名与密钥")
		default:
			s.fail(w, err)
		}
		return
	}
	if in.Restart {
		// Once configuration is committed, finish applying it even if the tab closes.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		if err := manager.Restart(ctx); err != nil {
			failure(w, 502, "agent_runtime_restart_failed", "配置已保存，但内置 Agent 重启失败，请检查程序目录、运行状态与日志后重试")
			return
		}
	}
	success(w, 200, manager.Status())
}

func (s *Server) adminRestartAgentRuntime(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.agentRuntimeOrFail(w)
	if !ok {
		return
	}
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	if err := manager.Restart(ctx); err != nil {
		if errors.Is(err, agentruntime.ErrNotConfigured) || errors.Is(err, agentruntime.ErrInvalidInstall) {
			failure(w, 400, "agent_runtime_unconfigured", "请先保存有效的采集端程序目录与配置")
		} else {
			failure(w, 502, "agent_runtime_restart_failed", "内置 Agent 重启失败，请检查运行状态与日志")
		}
		return
	}
	success(w, 200, manager.Status())
}

func (s *Server) adminStartAgentRuntime(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.agentRuntimeOrFail(w)
	if !ok {
		return
	}
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	if err := manager.Start(r.Context()); err != nil {
		switch {
		case errors.Is(err, agentruntime.ErrNotConfigured):
			failure(w, 400, "agent_runtime_unconfigured", "请先保存采集端工作路径")
		case errors.Is(err, agentruntime.ErrAlreadyRunning):
			failure(w, 409, "agent_runtime_running", "采集端进程已在运行")
		case errors.Is(err, agentruntime.ErrInvalidWorkDir):
			failure(w, 400, "invalid_work_dir", "工作路径必须存在，且包含 wzstudy-agent.exe 与 agent.env")
		default:
			s.fail(w, err)
		}
		return
	}
	success(w, 200, manager.Status())
}

func (s *Server) adminStopAgentRuntime(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.agentRuntimeOrFail(w)
	if !ok {
		return
	}
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	if err := manager.Stop(r.Context()); err != nil {
		if errors.Is(err, agentruntime.ErrNotRunning) {
			failure(w, 409, "agent_runtime_stopped", "采集端进程未运行")
			return
		}
		s.fail(w, err)
		return
	}
	success(w, 200, manager.Status())
}
