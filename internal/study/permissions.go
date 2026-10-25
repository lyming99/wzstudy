package study

import (
	"context"
	"errors"
	"slices"
)

const (
	PermissionRead   = "read"
	PermissionSync   = "sync"
	PermissionIngest = "ingest"
	PermissionUpload = "upload"
	PermissionInbox  = "inbox"
)

// nil is an old source's policy: preserve only its existing ingest/upload/inbox
// capabilities, never grant newly introduced reading or synchronization access.
// An explicit empty slice means no permissions, and must survive persistence.
func (s Source) EffectivePermissions() []string {
	if s.System {
		return []string{}
	}
	if s.Permissions == nil {
		return []string{PermissionIngest, PermissionUpload, PermissionInbox}
	}
	return slices.Clone(s.Permissions)
}

func (s Source) Allows(permission string) bool {
	return slices.Contains(s.EffectivePermissions(), permission)
}

func normalizePermissions(in []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	order := []string{PermissionRead, PermissionSync, PermissionIngest, PermissionUpload, PermissionInbox}
	if len(in) > len(order) {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, permission := range in {
		if !slices.Contains(order, permission) || seen[permission] {
			return nil, ErrInvalid
		}
		seen[permission] = true
	}
	out := []string{}
	for _, permission := range order {
		if seen[permission] {
			out = append(out, permission)
		}
	}
	return out, nil
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

// ClientForKey authenticates a managed manual source on published-content APIs.
// RSS keys and the internal studio source are never client reading credentials.
func (s *Service) ClientForKey(ctx context.Context, key, permission string) (out Source, err error) {
	if key == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		sources, e := tx.Sources()
		if e != nil {
			return e
		}
		for _, source := range sources {
			if source.Kind != "manual" || source.System || !EqualKey(source.KeyHash, key) {
				continue
			}
			if !source.Enabled {
				return ErrDisabled
			}
			if !source.Allows(permission) {
				return ErrForbidden
			}
			out = source
			return nil
		}
		return ErrUnauthorized
	})
	return
}

func cloneSource(v Source) Source {
	v.Permissions = slices.Clone(v.Permissions)
	return v
}
