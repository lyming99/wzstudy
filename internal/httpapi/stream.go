package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	after, limit, ids, err := changeArgs(r, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		failure(w, 503, "stream_limit", "实时连接已达上限，请稍后重连")
		return
	}
	page, err := s.service.Changes(r.Context(), after, limit, ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(event string, id int64, data any) error {
		b, e := json.Marshal(data)
		if e != nil {
			return e
		}
		_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, e = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, b); e != nil {
			return e
		}
		return controller.Flush()
	}
	if _, err = fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if err = controller.Flush(); err != nil {
		return
	}
	poll := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(10 * time.Second)
	expiry := time.NewTimer(30 * time.Minute)
	defer poll.Stop()
	defer heartbeat.Stop()
	defer expiry.Stop()
	firstPage := true
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expiry.C:
			return
		default:
		}
		for _, change := range page.Changes {
			if err = write("change", change.Cursor, change); err != nil {
				return
			}
		}
		// Checkpoints advance a filtered stream even when no events matched this source.
		// Clients must persist this id as well as the ids of ordinary changes.
		if page.NextCursor != after || firstPage {
			if err = write("checkpoint", page.NextCursor, map[string]int64{"cursor": page.NextCursor}); err != nil {
				return
			}
		}
		firstPage = false
		after = page.NextCursor
		if !page.HasMore {
			select {
			case <-r.Context().Done():
				return
			case <-expiry.C:
				return
			case <-heartbeat.C:
				_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
				if err = controller.Flush(); err != nil {
					return
				}
			case <-poll.C:
			}
		}
		page, err = s.service.Changes(r.Context(), after, limit, ids)
		if err != nil {
			_ = write("error", after, map[string]string{"code": "stream_unavailable"})
			return
		}
	}
}
