package httpapi

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"

	"wzstudy/internal/study"
)

// Every data request resolves to exactly one enabled user's workspace. System
// administrator/global reader keys intentionally have no implicit data access.
func (s *Server) dataRequest(r *http.Request, permission string) (*http.Request, error) {
	r = userCredentialRequest(r)
	key := bearer(r)
	user, err := s.service.UserForSession(r.Context(), key)
	if err == nil {
		if expected := r.Header.Get("X-WzStudy-User"); expected != "" && expected != user.ID {
			return r, study.ErrUnauthorized
		}
		return r.WithContext(study.WithWorkspace(r.Context(), user.ID)), nil
	}
	if !errors.Is(err, study.ErrUnauthorized) {
		return r, err
	}
	client, err := s.service.ClientForKey(r.Context(), key, permission)
	if err != nil {
		return r, err
	}
	if client.OwnerID == "" {
		return r, study.ErrUnauthorized
	}
	owner, err := s.service.User(r.Context(), client.OwnerID)
	if err != nil || !owner.Enabled {
		return r, study.ErrUnauthorized
	}
	return r.WithContext(study.WithWorkspace(r.Context(), owner.ID)), nil
}
func (s *Server) requirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scoped, err := s.dataRequest(r, permission)
			if err != nil {
				if errors.Is(err, study.ErrUnauthorized) {
					w.Header().Set("WWW-Authenticate", "Bearer")
				}
				s.fail(w, err)
				return
			}
			next.ServeHTTP(w, scoped)
		})
	}
}
func (s *Server) requireSourceWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source, err := s.service.Source(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			s.fail(w, study.ErrUnauthorized)
			return
		}
		if !study.EqualKey(source.KeyHash, bearer(r)) || source.OwnerID == "" {
			s.fail(w, study.ErrUnauthorized)
			return
		}
		owner, err := s.service.User(r.Context(), source.OwnerID)
		if err != nil || !owner.Enabled {
			s.fail(w, study.ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(study.WithWorkspace(r.Context(), owner.ID)))
	})
}

func (s *Server) requireQueryWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scoped, err := s.dataRequest(r, study.PermissionRead)
		if errors.Is(err, study.ErrForbidden) {
			scoped, err = s.dataRequest(r, study.PermissionSync)
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		next.ServeHTTP(w, scoped)
	})
}
