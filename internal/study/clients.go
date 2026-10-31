package study

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Client is a query identity, never an ingestion source or material owner.
type Client struct {
	OwnerID        string    `json:"owner_id,omitempty"`
	ID             string    `json:"id"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Enabled        bool      `json:"enabled"`
	Permissions    []string  `json:"permissions"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LegacySourceID string    `json:"legacy_source_id,omitempty"`
}

type ClientInput struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled,omitempty"`
	Permissions []string `json:"permissions"`
}

type clientRecord struct {
	Client Client `json:"client"`
	Secret struct {
		KeyHash       string `json:"key_hash"`
		KeyCiphertext string `json:"key_ciphertext,omitempty"`
	} `json:"internal"`
}

func normalizePolicy(in, order []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
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

func normalizeClient(in ClientInput) (ClientInput, error) {
	in.Slug, in.Name, in.Description = strings.TrimSpace(in.Slug), strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if !slugPattern.MatchString(in.Slug) || in.Name == "" || !validText(in.Name, 200) || !validText(in.Description, 4000) {
		return in, ErrInvalid
	}
	var err error
	in.Permissions, err = normalizePolicy(in.Permissions, []string{PermissionRead, PermissionSync, PermissionFeedback})
	return in, err
}

func cloneClient(v Client) Client {
	v.Permissions = slices.Clone(v.Permissions)
	return v
}

func (v Client) Allows(permission string) bool {
	return (permission == PermissionRead || permission == PermissionSync || permission == PermissionFeedback) && slices.Contains(v.Permissions, permission)
}

func newClientKey() (string, error) {
	key, err := NewKey()
	return strings.Replace(key, "wzsk_", "wzck_", 1), err
}

func saveClient(tx Tx, v Client, hash, ciphertext string) error {
	records, err := recordsAs[clientRecord](tx, "client")
	if err != nil {
		return err
	}
	for _, other := range records {
		if other.Client.ID != v.ID && (other.Client.Slug == v.Slug || (hash != "" && other.Secret.KeyHash == hash)) {
			return ErrConflict
		}
	}
	r := clientRecord{Client: cloneClient(v)}
	r.Secret.KeyHash = hash
	r.Secret.KeyCiphertext = ciphertext
	return recordSave(tx, "client", v.ID, r)
}

func (s *Service) Clients(ctx context.Context) (out []Client, err error) {
	out = []Client{}
	err = s.Store.View(ctx, func(tx Tx) error {
		records, e := recordsAs[clientRecord](tx, "client")
		if e != nil {
			return e
		}
		for _, r := range records {
			out = append(out, cloneClient(r.Client))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
		return nil
	})
	return
}

func (s *Service) Client(ctx context.Context, id string) (out Client, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		r, e := recordAs[clientRecord](tx, "client", id)
		out = cloneClient(r.Client)
		return e
	})
	return
}

func (s *Service) CreateClient(ctx context.Context, in ClientInput) (out Client, key string, err error) {
	in, err = normalizeClient(in)
	if err != nil {
		return
	}
	key, err = newClientKey()
	if err != nil {
		return
	}
	permissions := in.Permissions
	if permissions == nil {
		permissions = []string{PermissionRead, PermissionSync}
	}
	enabled := in.Enabled == nil || *in.Enabled
	now := s.Now()
	out = Client{OwnerID: WorkspaceUserID(ctx), ID: uuid.NewString(), Slug: in.Slug, Name: in.Name, Description: in.Description, Enabled: enabled, Permissions: slices.Clone(permissions), Revision: 1, CreatedAt: now, UpdatedAt: now}
	ciphertext, sealErr := s.sealClientKey(out, key)
	if sealErr != nil {
		key, err = "", sealErr
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error { return saveClient(tx, out, HashKey(key), ciphertext) })
	if err != nil {
		key = ""
	}
	return
}

func (s *Service) UpdateClient(ctx context.Context, id string, revision int64, in ClientInput) (out Client, err error) {
	in, err = normalizeClient(in)
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		r, e := recordAs[clientRecord](tx, "client", id)
		if e != nil {
			return e
		}
		v := r.Client
		if v.Revision != revision {
			return ErrConflict
		}
		if v.Slug != in.Slug {
			return ErrInvalid
		}
		v.Name, v.Description = in.Name, in.Description
		if in.Enabled != nil {
			v.Enabled = *in.Enabled
		}
		if in.Permissions != nil {
			v.Permissions = slices.Clone(in.Permissions)
		}
		v.Revision++
		v.UpdatedAt = s.Now()
		out = cloneClient(v)
		return saveClient(tx, v, r.Secret.KeyHash, r.Secret.KeyCiphertext)
	})
	return
}

func (s *Service) RotateClientKey(ctx context.Context, id string, revision int64) (out Client, key string, err error) {
	key, err = newClientKey()
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		r, e := recordAs[clientRecord](tx, "client", id)
		if e != nil {
			return e
		}
		if r.Client.Revision != revision {
			return ErrConflict
		}
		r.Client.Revision++
		r.Client.UpdatedAt = s.Now()
		out = cloneClient(r.Client)
		ciphertext, e := s.sealClientKey(r.Client, key)
		if e != nil {
			return e
		}
		return saveClient(tx, r.Client, HashKey(key), ciphertext)
	})
	if err != nil {
		key = ""
	}
	return
}

// Only independent clients may query. Legacy sources require an explicit,
// transactional migration; never fall back to source credentials here.
func clientAccess(tx Tx, key string, permissions ...string) (Client, error) {
	if strings.HasPrefix(key, "wzus_") {
		user, err := userLearningPrincipal(tx, key)
		if err != nil {
			return Client{}, err
		}
		for _, permission := range permissions {
			if user.Allows(permission) {
				return user, nil
			}
		}
		return Client{}, ErrForbidden
	}
	if key == "" {
		return Client{}, ErrUnauthorized
	}
	records, err := recordsAs[clientRecord](tx, "client")
	if err != nil {
		return Client{}, err
	}
	for _, record := range records {
		if !EqualKey(record.Secret.KeyHash, key) {
			continue
		}
		if !record.Client.Enabled {
			return Client{}, ErrDisabled
		}
		for _, permission := range permissions {
			if record.Client.Allows(permission) {
				return cloneClient(record.Client), nil
			}
		}
		return Client{}, ErrForbidden
	}
	return Client{}, ErrUnauthorized
}

func (s *Service) ClientForKey(ctx context.Context, key, permission string) (out Client, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = clientAccess(tx, key, permission); return e })
	return
}
