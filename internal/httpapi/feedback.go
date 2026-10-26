package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func (s *Server) reportFeedback(w http.ResponseWriter, r *http.Request) {
	key := bearer(r)
	if _, e := s.service.ClientForKey(r.Context(), key, study.PermissionFeedback); e != nil {
		s.fail(w, e)
		return
	}
	var in struct {
		Events []study.FeedbackInput `json:"events"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, e := s.service.ReportFeedback(r.Context(), key, in.Events)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}

func feedbackPageArgs(r *http.Request) (ids []string, limit, offset int, err error) {
	q := r.URL.Query()
	if q.Has("client_id") {
		return nil, 0, 0, study.ErrInvalid
	}
	limit, err = parseInt(q.Get("limit"), 20)
	if err != nil {
		return
	}
	offset, err = parseInt(q.Get("offset"), 0)
	if err != nil {
		return
	}
	ids = []string{}
	if q.Get("item_id") != "" {
		for _, raw := range strings.Split(q.Get("item_id"), ",") {
			id, e := uuid.Parse(raw)
			if e != nil || id == uuid.Nil {
				err = study.ErrInvalid
				return
			}
			ids = append(ids, id.String())
		}
	}
	if len(ids) > 100 {
		err = study.ErrInvalid
	}
	return
}
func (s *Server) clientFeedback(w http.ResponseWriter, r *http.Request) { s.feedbackPage(w, r, "") }
func (s *Server) adminClientFeedback(w http.ResponseWriter, r *http.Request) {
	s.feedbackPage(w, r, chi.URLParam(r, "id"))
}
func (s *Server) feedbackPage(w http.ResponseWriter, r *http.Request, id string) {
	ids, limit, offset, e := feedbackPageArgs(r)
	if e != nil {
		s.fail(w, e)
		return
	}
	out, e := s.service.ClientFeedback(r.Context(), bearer(r), id, ids, limit, offset)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}
func (s *Server) clientFeedbackEvents(w http.ResponseWriter, r *http.Request) {
	s.feedbackEvents(w, r, "")
}
func (s *Server) adminClientFeedbackEvents(w http.ResponseWriter, r *http.Request) {
	s.feedbackEvents(w, r, chi.URLParam(r, "id"))
}
func (s *Server) feedbackEvents(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	if q.Has("client_id") {
		s.fail(w, study.ErrInvalid)
		return
	}
	after := int64(0)
	var e error
	if q.Get("after") != "" {
		after, e = strconv.ParseInt(q.Get("after"), 10, 64)
		if e != nil {
			s.fail(w, study.ErrInvalid)
			return
		}
	}
	limit, e := parseInt(q.Get("limit"), 100)
	if e != nil {
		s.fail(w, e)
		return
	}
	out, e := s.service.FeedbackEvents(r.Context(), bearer(r), id, after, limit)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}

func feedbackQueryArgs(r *http.Request) (exclude []string, since *time.Time, include, used bool, err error) {
	q := r.URL.Query()
	exclude = []string{}
	if q.Has("client_id") {
		err = study.ErrInvalid
		return
	}
	for _, name := range []string{"exclude", "exclude_since", "unseen", "include_feedback"} {
		if q.Has(name) {
			used = true
			if len(q[name]) != 1 {
				err = study.ErrInvalid
				return
			}
		}
	}
	if q.Get("exclude") != "" {
		exclude = strings.Split(q.Get("exclude"), ",")
	}
	if study.ValidateFeedbackExclusions(exclude) != nil {
		err = study.ErrInvalid
		return
	}
	if q.Has("unseen") {
		value, e := strconv.ParseBool(q.Get("unseen"))
		if e != nil {
			err = study.ErrInvalid
			return
		}
		if value && !containsString(exclude, "seen") {
			exclude = append(exclude, "seen")
		}
	}
	if q.Has("include_feedback") {
		include, err = strconv.ParseBool(q.Get("include_feedback"))
		if err != nil {
			err = study.ErrInvalid
			return
		}
	}
	if q.Has("exclude_since") {
		value, e := time.Parse(time.RFC3339Nano, q.Get("exclude_since"))
		if e != nil || len(exclude) == 0 {
			err = study.ErrInvalid
			return
		}
		value = value.UTC()
		since = &value
	}
	return
}
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (s *Server) clientCandidateItems(w http.ResponseWriter, r *http.Request, id string) {
	f, e := filter(r, false)
	if e != nil {
		s.fail(w, e)
		return
	}
	exclude, since, include, _, e := feedbackQueryArgs(r)
	if e != nil {
		s.fail(w, e)
		return
	}
	out, e := s.service.ClientItems(r.Context(), bearer(r), id, f, exclude, since, include)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, out)
}
func (s *Server) adminClientItems(w http.ResponseWriter, r *http.Request) {
	s.clientCandidateItems(w, r, chi.URLParam(r, "id"))
}
