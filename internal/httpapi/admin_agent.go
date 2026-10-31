package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"wzstudy/internal/study"
)

func (s *Server) adminAgentStatus(w http.ResponseWriter, r *http.Request) {
	counts, err := s.service.AgentTaskSummary(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	agents, err := s.service.Agents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{
		"enabled":       s.cfg.AgentEnabled,
		"lease_seconds": s.cfg.AgentLeaseSeconds,
		"tasks":         counts,
		"agents":        agents,
	})
}

func (s *Server) adminAgents(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.Agents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminAgent(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.Agent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminCreateAgent(w http.ResponseWriter, r *http.Request) {
	var in study.AgentInput
	if !decode(w, r, &in) {
		return
	}
	v, key, err := s.service.CreateAgent(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 201, map[string]any{"agent": v, "access_key": key})
}

func (s *Server) adminUpdateAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		study.AgentInput
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.UpdateAgent(r.Context(), chi.URLParam(r, "id"), in.Revision, in.AgentInput)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminRotateAgentKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, key, err := s.service.RotateAgentKey(r.Context(), chi.URLParam(r, "id"), in.Revision)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"agent": v, "access_key": key})
}

func (s *Server) adminDeleteAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.service.DeleteAgent(r.Context(), id, in.Revision); err != nil {
		if errors.Is(err, study.ErrAgentInUse) {
			failure(w, http.StatusConflict, "agent_in_use", "该采集端还有未结束的任务，请先取消或删除这些任务后再删除身份")
			return
		}
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"id": id})
}

func (s *Server) adminCollectionTasks(w http.ResponseWriter, r *http.Request) {
	limit, err := parseInt(r.URL.Query().Get("limit"), 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	offset, err := parseInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	v, total, err := s.service.CollectionTasks(r.Context(), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"tasks": v, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminCreateCollectionTask(w http.ResponseWriter, r *http.Request) {
	var in study.CollectionTaskInput
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.CreateCollectionTask(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 201, v)
}

func (s *Server) adminCollectionTask(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.CollectionTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminCancelCollectionTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.CancelAgentTask(r.Context(), chi.URLParam(r, "id"), in.Reason)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminRetryCollectionTask(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.RetryAgentTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminCollectionTaskEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := parseInt(r.URL.Query().Get("limit"), 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.service.AgentTaskEvents(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminAgentTaskTemplates(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.AgentTaskTemplates(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminCreateAgentTaskTemplate(w http.ResponseWriter, r *http.Request) {
	var in study.AgentTaskTemplateInput
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.CreateAgentTaskTemplate(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 201, v)
}

func (s *Server) adminDeleteAgentTaskTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.DeleteAgentTaskTemplate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}
