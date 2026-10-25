package httpapi

import (
	"errors"
	"net/http"

	"wzstudy/internal/study"
)

// The shared read key is a compatibility credential. New clients use their
// administrator-managed source key, with an explicit permission per endpoint.
// A client credential never falls back to the administrator's browser cookie.
func (s *Server) readAccess(r *http.Request, permission string) error {
	if s.isAdmin(r) || study.EqualKey(study.HashKey(s.cfg.ReadKey), bearer(r)) {
		return nil
	}
	_, err := s.service.ClientForKey(r.Context(), bearer(r), permission)
	return err
}

func (s *Server) requirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := s.readAccess(r, permission); err != nil {
				if errors.Is(err, study.ErrUnauthorized) {
					w.Header().Set("WWW-Authenticate", "Bearer")
				}
				s.fail(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
