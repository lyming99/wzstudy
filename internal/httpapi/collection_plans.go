package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/study"
)

func (s *Server) collectionPlanRoutes(r chi.Router) {
	r.Get("/collection-plans", s.collectionPlans)
	r.Post("/collection-plans", s.createCollectionPlan)
	r.Post("/collection-plans/preview", s.previewCollectionPlan)
	r.Get("/collection-plans/{id}", s.collectionPlan)
	r.Put("/collection-plans/{id}", s.updateCollectionPlan)
	r.Post("/collection-plans/{id}/state", s.setCollectionPlanState)
	r.Post("/collection-plans/{id}/delete", s.deleteCollectionPlan)
	r.Post("/collection-plans/{id}/run", s.runCollectionPlan)
	r.Get("/collection-plans/{id}/runs", s.collectionPlanRuns)
	r.Post("/collection-plans/{id}/trigger-key", s.collectionPlanTriggerKey)
}

func (s *Server) collectionEnabled(w http.ResponseWriter) bool {
	if s.cfg.AgentEnabled {
		return true
	}
	failure(w, http.StatusForbidden, "collection_disabled", "采集模块未启用，不能创建执行任务")
	return false
}
func (s *Server) collectionPlans(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.CollectionPlans(r.Context(), r.URL.Query().Get("include_deleted") == "true")
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) collectionPlan(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.CollectionPlan(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) createCollectionPlan(w http.ResponseWriter, r *http.Request) {
	if !s.collectionEnabled(w) {
		return
	}
	var in study.CollectionPlanInput
	if !decodeLimit(w, r, &in, 32<<10) {
		return
	}
	out, err := s.service.CreateCollectionPlan(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 201, out)
}
func (s *Server) updateCollectionPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		study.CollectionPlanInput
	}
	if !decodeLimit(w, r, &in, 32<<10) {
		return
	}
	out, err := s.service.UpdateCollectionPlan(r.Context(), chi.URLParam(r, "id"), in.Revision, in.CollectionPlanInput)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) previewCollectionPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Schedule study.CollectionSchedule `json:"schedule"`
	}
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	out, err := s.service.PreviewCollectionPlan(r.Context(), in.Schedule, 5)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) setCollectionPlanState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		Enabled  *bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	out, err := s.service.SetCollectionPlanEnabled(r.Context(), chi.URLParam(r, "id"), in.Revision, *in.Enabled)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) deleteCollectionPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.service.DeleteCollectionPlan(r.Context(), chi.URLParam(r, "id"), in.Revision)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) runCollectionPlan(w http.ResponseWriter, r *http.Request) {
	if !s.collectionEnabled(w) {
		return
	}
	var in study.CollectionPlanTrigger
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	out, err := s.service.TriggerCollectionPlan(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) collectionPlanRuns(w http.ResponseWriter, r *http.Request) {
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
	out, total, err := s.service.CollectionPlanRuns(r.Context(), chi.URLParam(r, "id"), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"runs": out, "total": total, "limit": limit, "offset": offset})
}
func (s *Server) collectionPlanTriggerKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		Revoke   bool  `json:"revoke"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, key, err := s.service.RotateCollectionTriggerKey(r.Context(), chi.URLParam(r, "id"), in.Revision, in.Revoke)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"plan": out, "access_key": key})
}

func (s *Server) collectionWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.collectionEnabled(w) {
		return
	}
	if !s.triggerLimiter.allow(r.RemoteAddr, time.Now()) {
		w.Header().Set("Retry-After", "60")
		failure(w, 429, "trigger_rate_limited", "外部触发请求过于频繁，请稍后重试")
		return
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		failure(w, 401, "unauthorized", "需要计划专用触发凭据")
		return
	}
	var in study.CollectionPlanTrigger
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	out, err := s.service.TriggerCollectionWebhook(r.Context(), chi.URLParam(r, "id"), parts[1], in)
	if errors.Is(err, study.ErrTriggerRateLimited) {
		w.Header().Set("Retry-After", "60")
		failure(w, 429, "trigger_rate_limited", "同一计划每分钟最多接受一个新事件；相同事件可安全重试")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	// A trigger key grants no read access to plan config, logs or collected data.
	success(w, 200, map[string]any{"run_id": out.ID, "task_id": out.TaskID, "dispatch_status": out.DispatchStatus, "reason": out.Reason})
}
