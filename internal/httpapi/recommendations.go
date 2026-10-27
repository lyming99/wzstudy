package httpapi

import (
	"net/http"

	"wzstudy/internal/study"
)

func (s *Server) clientRecommendations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for name, values := range q {
		if (name != "limit" && name != "cursor") || len(values) != 1 {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	limit, err := parseInt(q.Get("limit"), 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.service.Recommendations(r.Context(), bearer(r), q.Get("cursor"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, out)
}
