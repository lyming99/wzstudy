package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"wzstudy/internal/study"
)

const clientConnectionType = "wzstudy.query_client"

// clientConnection authenticates the query Key itself. Administrator sessions,
// global legacy reader Keys and source credentials are deliberately not fallback
// credentials: a scanner must be able to verify the identity it is about to add.
func (s *Server) clientConnection(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(bearer(r), "wzus_") {
		s.fail(w, study.ErrUnauthorized)
		return
	}
	client, err := s.service.ClientForKey(r.Context(), bearer(r), study.PermissionRead)
	if errors.Is(err, study.ErrForbidden) {
		client, err = s.service.ClientForKey(r.Context(), bearer(r), study.PermissionSync)
	}
	if err != nil {
		if errors.Is(err, study.ErrUnauthorized) {
			failure(w, http.StatusUnauthorized, "unauthorized", "需要有效的查询客户端 Key")
		} else {
			s.fail(w, err)
		}
		return
	}
	if client.OwnerID == "" {
		s.fail(w, study.ErrUnauthorized)
		return
	}
	owner, ownerErr := s.service.User(r.Context(), client.OwnerID)
	if ownerErr != nil || !owner.Enabled {
		s.fail(w, study.ErrUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	success(w, http.StatusOK, map[string]any{
		"type":        clientConnectionType,
		"version":     1,
		"client_id":   client.ID,
		"name":        client.Name,
		"permissions": client.Permissions,
	})
}
