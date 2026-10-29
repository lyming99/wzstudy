package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/study"
)

// Capabilities authenticate the independent query identity. A legacy read key,
// administrator credential or ingestion source never becomes a learner.
func (s *Server) learningCapabilities(w http.ResponseWriter, r *http.Request) {
	client, err := s.service.ClientForKey(r.Context(), bearer(r), study.PermissionRead)
	if errors.Is(err, study.ErrForbidden) {
		client, err = s.service.ClientForKey(r.Context(), bearer(r), study.PermissionSync)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{
		"type": "wzstudy.learning", "version": 2, "client_id": client.ID, "user_id": client.ID, "identity": "user",
		"read": client.Allows(study.PermissionFeedback), "write": client.Allows(study.PermissionFeedback),
		"features":   []string{"view", "review", "review_plan", "records", "progress", "duration", "personal_collections", "legacy_feedback_import", "legacy_record_import", "legacy_review_import", "legacy_personal_collection_import", "drafts", "legacy_draft_import", "draft_history"},
		"max_events": 100,
	})
}

func (s *Server) reportLearning(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Events []study.LearningEventInput `json:"events"`
	}
	if !decodeLimit(w, r, &in, 1<<20) {
		return
	}
	out, err := s.service.ReportLearning(r.Context(), bearer(r), in.Events)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

// Legacy records are imported only on an explicit request and cannot target a
// caller-supplied Client. Ownership and user attestation remain client duties.
func (s *Server) importLegacyRecords(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	key := bearer(r)
	if _, err := s.service.ClientForKey(r.Context(), key, study.PermissionFeedback); err != nil {
		s.fail(w, err)
		return
	}
	var input struct {
		Records []study.LegacyRecordInput `json:"records"`
	}
	if !decodeLimit(w, r, &input, 1<<20) {
		return
	}
	out, err := s.service.ImportLegacyRecords(r.Context(), key, input.Records)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

// Only user-confirmed pending reviews are imported; the server never infers a
// completed review from a completed local todo.
func (s *Server) importLegacyReviews(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	key := bearer(r)
	if _, err := s.service.ClientForKey(r.Context(), key, study.PermissionFeedback); err != nil {
		s.fail(w, err)
		return
	}
	var input struct {
		Reviews []study.LegacyReviewInput `json:"reviews"`
	}
	if !decodeLimit(w, r, &input, 1<<20) {
		return
	}
	out, err := s.service.ImportLegacyReviews(r.Context(), key, input.Reviews)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

// Independent clients with sync-only access can read aggregate counts for a
// published item they already obtained through changes, without granting the
// read permission or exposing another client's private state.
func (s *Server) clientLearningItemStats(w http.ResponseWriter, r *http.Request) {
	_, err := s.service.ClientForKey(r.Context(), bearer(r), study.PermissionRead)
	if errors.Is(err, study.ErrForbidden) {
		_, err = s.service.ClientForKey(r.Context(), bearer(r), study.PermissionSync)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.service.LearningStats(r.Context(), chi.URLParam(r, "item_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) learningItemStats(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.LearningStats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) clientLearning(w http.ResponseWriter, r *http.Request) { s.learningPage(w, r, "") }
func (s *Server) adminClientLearning(w http.ResponseWriter, r *http.Request) {
	s.learningPage(w, r, chi.URLParam(r, "id"))
}
func (s *Server) learningPage(w http.ResponseWriter, r *http.Request, adminClientID string) {
	ids, limit, offset, err := feedbackPageArgs(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	var from, until *time.Time
	if q.Has("from") != q.Has("until") || len(q["from"]) > 1 || len(q["until"]) > 1 {
		s.fail(w, study.ErrInvalid)
		return
	}
	if q.Has("from") {
		start, startErr := time.Parse(time.RFC3339Nano, q.Get("from"))
		end, endErr := time.Parse(time.RFC3339Nano, q.Get("until"))
		if startErr != nil || endErr != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
		start, end = start.UTC(), end.UTC()
		from, until = &start, &end
	}
	out, err := s.service.ClientLearningPeriod(r.Context(), bearer(r), adminClientID, ids, limit, offset, from, until)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) clientLearningRecord(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	out, err := s.service.ClientLearningRecord(r.Context(), bearer(r), chi.URLParam(r, "item_id"), chi.URLParam(r, "record_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) clientLearningRecords(w http.ResponseWriter, r *http.Request) {
	s.learningRecords(w, r, "")
}
func (s *Server) adminClientLearningRecords(w http.ResponseWriter, r *http.Request) {
	s.learningRecords(w, r, chi.URLParam(r, "id"))
}
func (s *Server) learningRecords(w http.ResponseWriter, r *http.Request, adminClientID string) {
	if r.URL.Query().Has("client_id") {
		s.fail(w, study.ErrInvalid)
		return
	}
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
	out, err := s.service.ClientLearningRecords(r.Context(), bearer(r), adminClientID, chi.URLParam(r, "item_id"), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) clientLearningEvents(w http.ResponseWriter, r *http.Request) {
	s.learningEvents(w, r, "")
}
func (s *Server) adminClientLearningEvents(w http.ResponseWriter, r *http.Request) {
	s.learningEvents(w, r, chi.URLParam(r, "id"))
}
func (s *Server) learningEvents(w http.ResponseWriter, r *http.Request, adminClientID string) {
	if r.URL.Query().Has("client_id") {
		s.fail(w, study.ErrInvalid)
		return
	}
	after := int64(0)
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	limit, err := parseInt(r.URL.Query().Get("limit"), 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.service.ClientLearningEvents(r.Context(), bearer(r), adminClientID, after, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
