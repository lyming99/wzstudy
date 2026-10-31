package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"wzstudy/internal/study"
)

func (s *Server) adminDeleteCollectionTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.service.DeleteCollectionTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, study.ErrBusy) {
			failure(w, 409, "task_not_finished", "只能删除已结束的任务，请先取消运行中的任务")
			return
		}
		s.fail(w, err)
		return
	}
	success(w, 200, task)
}
