package httpapi

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"wzstudy/internal/study"
)

func (s *Server) agentCheckDuplicates(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Attempt int                        `json:"attempt"`
		Items   []study.CollectionIdentity `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Attempt < 1 {
		s.fail(w, study.ErrInvalid)
		return
	}
	out, err := s.service.CheckCollectionDuplicates(r.Context(), agentFromRequest(r).ID, chi.URLParam(r, "id"), in.Attempt, in.Items)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"items": out})
}
