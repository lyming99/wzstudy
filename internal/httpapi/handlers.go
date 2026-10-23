package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/study"
)

func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Sources(r.Context())
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) source(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Source(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var in study.SourceInput
	if !decode(w, r, &in) {
		return
	}
	v, key, e := s.service.CreateSource(r.Context(), in)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 201, map[string]any{"source": v, "access_key": key})
}
func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		study.SourceInput
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.UpdateSource(r.Context(), chi.URLParam(r, "id"), in.Revision, in.SourceInput)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, key, e := s.service.RotateKey(r.Context(), chi.URLParam(r, "id"), in.Revision)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, map[string]any{"source": v, "access_key": key})
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if s.worker == nil {
		failure(w, 503, "polling_unavailable", "采集器不可用")
		return
	}
	out, e := s.worker.Once(r.Context(), chi.URLParam(r, "id"), true)
	if e != nil {
		if e == study.ErrNotFound || e == study.ErrInvalid || e == study.ErrDisabled || e == study.ErrBusy || e == study.ErrConflict {
			s.fail(w, e)
		} else {
			failure(w, 502, "feed_fetch_failed", "采集失败，请检查数据源地址及 Feed 格式")
		}
		return
	}
	success(w, 200, out)
}
func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	key := bearer(r)
	if key == "" {
		failure(w, 401, "unauthorized", "需要数据源专属 Bearer 密钥")
		return
	}
	var in struct {
		Items []study.Content `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, e := s.service.Ingest(r.Context(), chi.URLParam(r, "id"), key, in.Items)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}
func (s *Server) adminItems(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r, true)
	if e != nil {
		s.fail(w, e)
		return
	}
	v, e := s.service.Items(r.Context(), f)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, map[string]any{"items": v, "limit": f.Limit, "offset": f.Offset})
}
func (s *Server) adminItem(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Item(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request)   { s.publication(w, r, true) }
func (s *Server) unpublish(w http.ResponseWriter, r *http.Request) { s.publication(w, r, false) }
func (s *Server) publication(w http.ResponseWriter, r *http.Request, publish bool) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.Publication(r.Context(), chi.URLParam(r, "id"), in.Revision, publish)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) publicSources(w http.ResponseWriter, r *http.Request) {
	sources, e := s.service.Sources(r.Context())
	if e != nil {
		s.fail(w, e)
		return
	}
	out := make([]map[string]any, 0, len(sources))
	for _, v := range sources {
		out = append(out, map[string]any{"id": v.ID, "slug": v.Slug, "name": v.Name, "description": v.Description, "category": v.Category, "language": v.Language})
	}
	success(w, 200, out)
}
func (s *Server) publicItems(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r, false)
	if e != nil {
		s.fail(w, e)
		return
	}
	items, e := s.service.Items(r.Context(), f)
	if e != nil {
		s.fail(w, e)
		return
	}
	out := make([]*study.PublicItem, 0, len(items))
	for _, v := range items {
		out = append(out, v.Published)
	}
	success(w, 200, map[string]any{"items": out, "limit": f.Limit, "offset": f.Offset})
}
func (s *Server) publicItem(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Item(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		s.fail(w, e)
		return
	}
	if v.Published == nil {
		s.fail(w, study.ErrNotFound)
		return
	}
	success(w, 200, v.Published)
}
func changeArgs(r *http.Request, resume bool) (int64, int, []string, error) {
	raw := r.URL.Query().Get("after")
	if resume && r.Header.Get("Last-Event-ID") != "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		raw = "0"
	}
	after, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || after < 0 {
		return 0, 0, nil, study.ErrInvalid
	}
	limit, e := parseInt(r.URL.Query().Get("limit"), 100)
	if e != nil {
		return 0, 0, nil, e
	}
	ids, e := sourceIDs(r.URL.Query().Get("source_id"))
	return after, limit, ids, e
}
func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	after, limit, ids, e := changeArgs(r, false)
	if e != nil {
		s.fail(w, e)
		return
	}
	v, e := s.service.Changes(r.Context(), after, limit, ids)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
