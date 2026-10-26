package study

import (
	"context"
	"errors"
	"slices"
)

const (
	PermissionFeedback = "feedback"
	PermissionRead     = "read"
	PermissionSync     = "sync"
	PermissionIngest   = "ingest"
	PermissionUpload   = "upload"
)

// nil is an old source's policy: preserve its existing ingest/upload capabilities,
// never grant reading or synchronization access. Legacy query grants require
// an explicit migration to Client; inbox is retired. Source grants are write-only.
// An explicit empty slice means no permissions, and must survive persistence.
func (s Source) EffectivePermissions() []string {
	if s.System {
		return []string{}
	}
	if s.Permissions == nil {
		return []string{PermissionIngest, PermissionUpload}
	}
	return legacyPolicy(s, []string{PermissionIngest, PermissionUpload})
}

func (s Source) Allows(permission string) bool {
	return slices.Contains(s.EffectivePermissions(), permission)
}

func normalizePermissions(in []string) ([]string, error) {
	return normalizePolicy(in, []string{PermissionIngest, PermissionUpload})
}

func sourceAccess(tx Tx, id, key string, permissions ...string) (Source, error) {
	source, err := tx.Source(id)
	if errors.Is(err, ErrNotFound) {
		return Source{}, ErrUnauthorized
	}
	if err != nil {
		return Source{}, err
	}
	if source.System || !EqualKey(source.KeyHash, key) {
		return Source{}, ErrUnauthorized
	}
	if !source.Enabled {
		return Source{}, ErrDisabled
	}
	for _, permission := range permissions {
		if !source.Allows(permission) {
			return Source{}, ErrForbidden
		}
	}
	return source, nil
}

// AuthorizeSource is a cheap upload preflight; writes repeat the check inside
// the same transaction as the mutation so a revoked policy cannot be bypassed.
func (s *Service) AuthorizeSource(ctx context.Context, id, key string, permissions ...string) (out Source, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		out, err = sourceAccess(tx, id, key, permissions...)
		return err
	})
	return
}

func sourcePolicy(v Source) Source {
	if v.Permissions != nil {
		v.Permissions = v.EffectivePermissions()
	}
	return v
}

func cloneSource(v Source) Source {
	v.Permissions = slices.Clone(v.Permissions)
	return v
}
