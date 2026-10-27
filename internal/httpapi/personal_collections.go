package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/study"
)

// Imports one source-specific subset. Only the authenticated Client is used;
// ownership attestation and source splitting are the caller's responsibility.
func (s *Server) importLegacyPersonalCollection(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	key := bearer(r)
	if _, err := s.service.ClientForKey(r.Context(), key, study.PermissionFeedback); err != nil {
		s.fail(w, err)
		return
	}
	var input study.LegacyPersonalCollectionInput
	if !decodeLimit(w, r, &input, 1<<20) {
		return
	}
	out, err := s.service.ImportLegacyPersonalCollection(r.Context(), key, input)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}

func (s *Server) personalCollectionCommand(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	var input study.PersonalCollectionCommand
	if !decodeLimit(w, r, &input, 1<<16) {
		return
	}
	result, err := s.service.ApplyPersonalCollection(r.Context(), bearer(r), input)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, result)
}

func (s *Server) personalCollections(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "limit" && key != "offset") || len(values) != 1 {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	limit, err := parseInt(query.Get("limit"), 20)
	if err != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	offset, err := parseInt(query.Get("offset"), 0)
	if err != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	result, err := s.service.PersonalCollections(r.Context(), bearer(r), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, result)
}

func (s *Server) personalCollection(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	result, err := s.service.PersonalCollection(r.Context(), bearer(r), chi.URLParam(r, "collection_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, result)
}
