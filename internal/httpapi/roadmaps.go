package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"wzstudy/internal/study"
)

func (s *Server) learningRoadmaps(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := catalogPageArgs(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	page, err := s.service.LearningRoadmaps(r.Context(), r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, http.StatusOK, page)
}

func (s *Server) learningRoadmap(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.LearningRoadmap(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, http.StatusOK, out)
}

func (s *Server) saveLearningRoadmap(w http.ResponseWriter, r *http.Request) {
	var in study.RoadmapInput
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	out, err := s.service.SaveLearningRoadmap(r.Context(), id, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	success(w, status, out)
}

func (s *Server) deleteLearningRoadmap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.service.DeleteLearningRoadmap(r.Context(), chi.URLParam(r, "id"), in.Revision); err != nil {
		s.fail(w, err)
		return
	}
	success(w, http.StatusOK, map[string]bool{"deleted": true})
}
