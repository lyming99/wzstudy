package httpapi

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
	"wzstudy/internal/study"
)

func (s *Server) learningDraft(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if key != "parent_id" || len(values) != 1 || values[0] == "" {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	out, err := s.service.ReadLearningDraft(r.Context(), bearer(r), chi.URLParam(r, "item_id"), query.Get("parent_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) learningDraftEvent(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	out, err := s.service.ReadLearningDraftEvent(r.Context(), bearer(r), chi.URLParam(r, "event_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) learningDraftHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, offset := 20, 0
	for key, values := range query {
		if (key != "parent_id" && key != "limit" && key != "offset") || len(values) != 1 || values[0] == "" {
			s.fail(w, study.ErrInvalid)
			return
		}
		var err error
		if key == "limit" {
			limit, err = strconv.Atoi(values[0])
		}
		if key == "offset" {
			offset, err = strconv.Atoi(values[0])
		}
		if err != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	out, err := s.service.ListLearningDraftHistory(r.Context(), bearer(r), chi.URLParam(r, "item_id"), query.Get("parent_id"), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) learningDraftCommand(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	key := bearer(r)
	if _, err := s.service.ClientForKey(r.Context(), key, study.PermissionFeedback); err != nil {
		s.fail(w, err)
		return
	}
	var input study.LearningDraftCommand
	if !decodeLimit(w, r, &input, 1<<16) {
		return
	}
	out, err := s.service.ApplyLearningDraft(r.Context(), key, input)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
func (s *Server) importLegacyLearningDraft(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	key := bearer(r)
	if _, err := s.service.ClientForKey(r.Context(), key, study.PermissionFeedback); err != nil {
		s.fail(w, err)
		return
	}
	var input study.LegacyDraftInput
	if !decodeLimit(w, r, &input, 1<<16) {
		return
	}
	out, err := s.service.ImportLegacyDraft(r.Context(), key, input)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
