package httpapi

import (
	"errors"
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
	w.Header().Set("Cache-Control", "private, no-store")
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

// clientSecret is restricted to the signed-in owner's workspace. Secret
// material never appears in ordinary client lists, detail or public endpoints.
func (s *Server) clientSecret(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	key, err := s.service.ClientSecret(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, http.StatusOK, map[string]any{"available": key != "", "access_key": key})
}

// rememberClientKey restores a previously saved old key without rotating it.
// The service compares it with the current hash within the write transaction.
func (s *Server) rememberClientKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	var in struct {
		AccessKey string `json:"access_key"`
	}
	if !decodeLimit(w, r, &in, 4096) {
		return
	}
	err := s.service.RememberClientKey(r.Context(), chi.URLParam(r, "id"), in.AccessKey)
	switch {
	case errors.Is(err, study.ErrClientKeyMismatch):
		failure(w, http.StatusBadRequest, "client_key_mismatch", "Key 与此客户端当前 Key 不匹配，未保存也未轮换")
	case errors.Is(err, study.ErrClientVaultUnavailable):
		failure(w, http.StatusServiceUnavailable, "client_key_vault_unavailable", "密钥保存功能未配置，暂时无法保存")
	case err != nil:
		s.fail(w, err)
	default:
		success(w, http.StatusOK, map[string]bool{"saved": true})
	}
}

func (s *Server) rotateClientKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
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
