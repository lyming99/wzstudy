package httpapi

import (
	"net/http"
	"wzstudy/internal/study"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) clients(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Clients(r.Context())
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) client(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Client(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	var in study.ClientInput
	if !decode(w, r, &in) {
		return
	}
	v, key, e := s.service.CreateClient(r.Context(), in)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 201, map[string]any{"client": v, "access_key": key})
}
func (s *Server) updateClient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		study.ClientInput
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.UpdateClient(r.Context(), chi.URLParam(r, "id"), in.Revision, in.ClientInput)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) rotateClientKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, key, e := s.service.RotateClientKey(r.Context(), chi.URLParam(r, "id"), in.Revision)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, map[string]any{"client": v, "access_key": key})
}
func (s *Server) previewIdentityMigration(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.PreviewIdentityMigration(r.Context())
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) migrateIdentities(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Choices *[]study.IdentityMigrationChoice `json:"choices"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Choices == nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	v, e := s.service.MigrateIdentities(r.Context(), *in.Choices)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) intakeHistory(w http.ResponseWriter, r *http.Request) {
	limit, e := parseInt(r.URL.Query().Get("limit"), 20)
	if e != nil {
		s.fail(w, e)
		return
	}
	offset, e := parseInt(r.URL.Query().Get("offset"), 0)
	if e != nil {
		s.fail(w, e)
		return
	}
	source := r.URL.Query().Get("source_id")
	if source != "" {
		if _, e = uuid.Parse(source); e != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	v, e := s.service.IntakeHistory(r.Context(), source, limit, offset)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
