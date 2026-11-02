package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"wzstudy/internal/study"
)

// Only documented, singular query arguments are accepted. In particular a
// caller cannot select another learner or workspace through this endpoint.
func modeQueryValid(r *http.Request, allowed ...string) bool {
	valid := map[string]bool{}
	for _, key := range allowed {
		valid[key] = true
	}
	for key, values := range r.URL.Query() {
		if !valid[key] || len(values) != 1 {
			return false
		}
	}
	return true
}
func (s *Server) learningModeCategories(w http.ResponseWriter, r *http.Request) {
	if !modeQueryValid(r, "mode", "limit", "offset") {
		s.fail(w, study.ErrInvalid)
		return
	}
	q := r.URL.Query()
	limit, e := parseInt(q.Get("limit"), 50)
	if e != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	offset, e := parseInt(q.Get("offset"), 0)
	if e != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	out, e := s.service.LearningModeCategories(r.Context(), bearer(r), q.Get("mode"), limit, offset)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}
func (s *Server) learningModeItems(w http.ResponseWriter, r *http.Request) {
	if !modeQueryValid(r, "mode", "category", "collection_id", "roadmap_id", "node_id", "q", "seed", "limit", "offset") {
		s.fail(w, study.ErrInvalid)
		return
	}
	q := r.URL.Query()
	limit, e := parseInt(q.Get("limit"), 30)
	if e != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	offset, e := parseInt(q.Get("offset"), 0)
	if e != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	seed := int64(0)
	if q.Has("seed") {
		seed, e = strconv.ParseInt(q.Get("seed"), 10, 64)
		if e != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	out, e := s.service.LearningModeItems(r.Context(), bearer(r), study.LearningModeFilter{Mode: q.Get("mode"), Category: q.Get("category"), CollectionID: q.Get("collection_id"), RoadmapID: q.Get("roadmap_id"), NodeID: q.Get("node_id"), Query: q.Get("q"), Seed: seed, Limit: limit, Offset: offset})
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}

// A client Key can store learning state only when explicitly granted feedback.
// Its state stays scoped to that Client, not silently merged into its owner or
// any user's login. Read-only Keys retain the existing unauthorized boundary.
func (s *Server) requireLearningWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Preserve the existing session CSRF, expected-user and tenant-selector
		// checks. Do not turn a cookie into a Bearer request before this check.
		if r.Header.Get("Authorization") == "" || strings.HasPrefix(bearer(r), "wzus_") {
			s.requireUser(http.HandlerFunc(func(w http.ResponseWriter, authenticated *http.Request) {
				next.ServeHTTP(w, authenticated.WithContext(study.WithSharedLearning(authenticated.Context())))
			})).ServeHTTP(w, r)
			return
		}
		if r.URL.Query().Has("client_id") || r.URL.Query().Has("user_id") || r.URL.Query().Has("owner_id") {
			s.fail(w, study.ErrInvalid)
			return
		}
		scoped, e := s.dataRequest(r, study.PermissionFeedback)
		if e != nil {
			if e == study.ErrForbidden {
				e = study.ErrUnauthorized
			}
			s.fail(w, e)
			return
		}
		next.ServeHTTP(w, scoped)
	})
}
