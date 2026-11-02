package httpapi

import (
	"net/http"
	"wzstudy/internal/study"
)

func (s *Server) organizeItems(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		s.fail(w, study.ErrInvalid)
		return
	}
	var in study.ItemOrganizationInput
	if !decodeLimit(w, r, &in, 1<<20) {
		return
	}
	out, err := s.service.OrganizeItems(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, http.StatusOK, out)
}
