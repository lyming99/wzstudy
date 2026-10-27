package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/study"
)

func catalogPageArgs(r *http.Request) (int, int, error) {
	limit, err := parseInt(r.URL.Query().Get("limit"), 20)
	if err != nil {
		return 0, 0, err
	}
	offset, err := parseInt(r.URL.Query().Get("offset"), 0)
	return limit, offset, err
}

func (s *Server) publicCatalogCollections(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := catalogPageArgs(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	page, err := s.service.PublicCatalogCollections(r.Context(), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, page)
}

func (s *Server) publicCatalogCollection(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.PublicCatalogCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) catalogChanges(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	limit, err := parseInt(r.URL.Query().Get("limit"), 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.service.CatalogChanges(r.Context(), after, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminCatalogCollections(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := catalogPageArgs(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.service.AdminCatalogCollections(r.Context(), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) adminCatalogCollection(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.AdminCatalogCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}

func (s *Server) saveCatalogCollection(w http.ResponseWriter, r *http.Request) {
	var in study.CatalogInput
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	v, err := s.service.SaveCatalogCollection(r.Context(), id, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	success(w, status, v)
}

func (s *Server) publishCatalogCollection(w http.ResponseWriter, r *http.Request) {
	s.catalogPublication(w, r, true)
}
func (s *Server) unpublishCatalogCollection(w http.ResponseWriter, r *http.Request) {
	s.catalogPublication(w, r, false)
}
func (s *Server) catalogPublication(w http.ResponseWriter, r *http.Request, publish bool) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.service.PublishCatalogCollection(r.Context(), chi.URLParam(r, "id"), in.Revision, publish)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, v)
}
func (s *Server) deleteCatalogCollection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.service.DeleteCatalogCollection(r.Context(), chi.URLParam(r, "id"), in.Revision); err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]bool{"deleted": true})
}
