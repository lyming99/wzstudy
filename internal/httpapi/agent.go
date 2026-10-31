package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"wzstudy/internal/study"
)

type agentContextKey struct{}

func agentFromRequest(r *http.Request) study.Agent {
	agent, _ := r.Context().Value(agentContextKey{}).(study.Agent)
	return agent
}

// requireAgent authenticates a collection agent with its own key. The owner
// workspace is injected only after the identity is verified, so task queries
// remain tenant-scoped. A disabled feature is indistinguishable from a missing
// route.
func (s *Server) requireAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AgentEnabled {
			s.fail(w, study.ErrNotFound)
			return
		}
		key := bearer(r)
		if key == "" {
			failure(w, 401, "unauthorized", "需要采集端专属 Bearer 密钥")
			return
		}
		agent, err := s.service.AgentForKey(r.Context(), key, study.PermissionTaskClaim)
		if err != nil {
			if errors.Is(err, study.ErrUnauthorized) {
				failure(w, http.StatusUnauthorized, "unauthorized", "采集端身份或密钥无效：身份可能已删除或密钥已轮换，请更新 agent.env 中的 WZSTUDY_AGENT_KEY（不是模型 API Key）")
			} else {
				s.fail(w, err)
			}
			return
		}
		ctx := study.WithWorkspace(r.Context(), agent.OwnerID)
		ctx = context.WithValue(ctx, agentContextKey{}, agent)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withAdminWorkspace binds collection management to the administrator's own
// workspace. It runs after require(true), so the identity is already verified.
func (s *Server) withAdminWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		administrator, err := s.service.Administrator(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(study.WithWorkspace(r.Context(), administrator.ID)))
	})
}

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	success(w, 200, map[string]any{
		"agent":         agent,
		"server_time":   time.Now().UTC(),
		"lease_seconds": s.cfg.AgentLeaseSeconds,
	})
}

func (s *Server) agentUpload(w http.ResponseWriter, r *http.Request) {
	if _, err := s.service.AgentForKey(r.Context(), bearer(r), study.PermissionIngest); err != nil {
		s.fail(w, err)
		return
	}
	assets, cleanup, err := s.readUploads(w, r)
	if err != nil {
		cleanup()
		s.uploadFailure(w, err)
		return
	}
	if len(assets) != 1 {
		cleanup()
		s.fail(w, study.ErrInvalid)
		return
	}
	asset := assets[0]
	if err := s.service.RegisterUploadedAsset(r.Context(), asset); err != nil {
		cleanup()
		s.fail(w, err)
		return
	}
	success(w, 201, asset)
}

func (s *Server) agentClaim(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	var in struct {
		Capacity    int    `json:"capacity"`
		WaitSeconds int    `json:"wait_seconds"`
		Version     string `json:"version"`
		Hostname    string `json:"hostname"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Capacity < 0 || in.Capacity > 4 || in.WaitSeconds < 0 || in.WaitSeconds > 25 {
		s.fail(w, study.ErrInvalid)
		return
	}
	if err := s.service.TouchAgent(r.Context(), agent.ID, in.Version, in.Hostname, bearer(r)); err != nil && err != study.ErrNotFound {
		s.fail(w, err)
		return
	}
	deadline := time.Now().Add(time.Duration(in.WaitSeconds) * time.Second)
	for {
		task, ok, err := s.service.ClaimAgentTask(r.Context(), agent.ID, in.Capacity)
		if err != nil {
			s.fail(w, err)
			return
		}
		if ok {
			success(w, 200, map[string]any{"task": task})
			return
		}
		if !time.Now().Before(deadline) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	task, err := s.service.HeartbeatAgentTask(r.Context(), agent.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{
		"lease_expires_at": task.LeaseExpiresAt,
		"cancel_requested": task.CancelRequested,
		"cancel_reason":    task.CancelReason,
		"status":           task.Status,
	})
}

func (s *Server) agentEvents(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	var in struct {
		Attempt int                     `json:"attempt"`
		Events  []study.AgentEventInput `json:"events"`
	}
	if !decodeLimit(w, r, &in, 1<<20) {
		return
	}
	if in.Attempt < 1 {
		s.fail(w, study.ErrInvalid)
		return
	}
	accepted, highest, err := s.service.AppendAgentEvents(r.Context(), agent.ID, chi.URLParam(r, "id"), in.Attempt, in.Events)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"accepted": accepted, "highest_seq": highest})
}

func (s *Server) agentPushItems(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	var in struct {
		Attempt int             `json:"attempt"`
		Items   []study.Content `json:"items"`
	}
	if !decodeLimit(w, r, &in, 8<<20) {
		return
	}
	if in.Attempt < 1 {
		s.fail(w, study.ErrInvalid)
		return
	}
	receipt, err := s.service.IngestAgentTaskItems(r.Context(), agent.ID, chi.URLParam(r, "id"), in.Attempt, in.Items)
	if err != nil {
		if errors.Is(err, study.ErrContentType) {
			failure(w, 400, "content_type_mismatch", "任务要求的数据类型与提交内容不匹配：视频/音频任务必须包含对应的媒体文件")
			return
		}
		s.fail(w, err)
		return
	}
	success(w, 200, receipt)
}

func (s *Server) agentComplete(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	var in struct {
		Attempt int              `json:"attempt"`
		Status  string           `json:"status"`
		Summary string           `json:"summary"`
		Stats   study.TaskStats  `json:"stats"`
		Error   *study.TaskError `json:"error"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Attempt < 1 {
		s.fail(w, study.ErrInvalid)
		return
	}
	if err := s.service.CompleteAgentTask(r.Context(), agent.ID, chi.URLParam(r, "id"), in.Attempt, in.Status, in.Summary, in.Stats, in.Error); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentRelease(w http.ResponseWriter, r *http.Request) {
	agent := agentFromRequest(r)
	var in struct {
		Attempt int    `json:"attempt"`
		Reason  string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Attempt < 1 {
		s.fail(w, study.ErrInvalid)
		return
	}
	if err := s.service.ReleaseAgentTask(r.Context(), agent.ID, chi.URLParam(r, "id"), in.Attempt, in.Reason); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
