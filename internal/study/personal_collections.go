package study

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// PersonalCollection belongs to one independent query Client. A published
// administrator catalogue is a separate type and never shares this namespace.
type PersonalCollection struct {
	ID        string     `json:"id"`
	ClientID  string     `json:"client_id"`
	Title     string     `json:"title"`
	Revision  int64      `json:"revision"`
	Items     []string   `json:"items"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type PersonalCollectionCommand struct {
	EventID          string   `json:"event_id"`
	CollectionID     string   `json:"collection_id"`
	Action           string   `json:"action"`
	ExpectedRevision int64    `json:"expected_revision"`
	Title            string   `json:"title,omitempty"`
	ItemID           string   `json:"item_id,omitempty"`
	Order            []string `json:"order,omitempty"`
}

type PersonalCollectionAck struct {
	EventID    string             `json:"event_id"`
	Duplicate  bool               `json:"duplicate"`
	Collection PersonalCollection `json:"collection"`
}

type personalCollectionEvent struct {
	Fingerprint string             `json:"fingerprint"`
	Collection  PersonalCollection `json:"collection"`
}

type PersonalCollectionItem struct {
	ItemID    string `json:"item_id"`
	Available bool   `json:"available"`
}

type PersonalCollectionView struct {
	ID        string                   `json:"id"`
	ClientID  string                   `json:"client_id"`
	Title     string                   `json:"title"`
	Revision  int64                    `json:"revision"`
	Items     []PersonalCollectionItem `json:"items"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
}

type PersonalCollectionPage struct {
	ClientID string                   `json:"client_id"`
	Items    []PersonalCollectionView `json:"items"`
	Total    int                      `json:"total"`
	Offset   int                      `json:"offset"`
	Limit    int                      `json:"limit"`
}

func normalizePersonalCommand(in PersonalCollectionCommand) (PersonalCollectionCommand, error) {
	var ok bool
	in.EventID, ok = validCatalogID(in.EventID)
	if !ok {
		return in, ErrInvalid
	}
	in.CollectionID, ok = validCatalogID(in.CollectionID)
	if !ok || in.ExpectedRevision < 0 {
		return in, ErrInvalid
	}
	in.Title = strings.TrimSpace(in.Title)
	switch in.Action {
	case "create", "rename":
		if in.Title == "" || !validText(in.Title, 500) || in.ItemID != "" || len(in.Order) != 0 {
			return in, ErrInvalid
		}
		if in.Action == "create" && in.ExpectedRevision != 0 || in.Action == "rename" && in.ExpectedRevision == 0 {
			return in, ErrInvalid
		}
	case "add", "remove":
		in.ItemID, ok = validCatalogID(in.ItemID)
		if !ok || in.ExpectedRevision == 0 || in.Title != "" || len(in.Order) != 0 {
			return in, ErrInvalid
		}
	case "reorder":
		if in.ExpectedRevision == 0 || in.Title != "" || in.ItemID != "" || len(in.Order) > 1000 {
			return in, ErrInvalid
		}
		ids := make(map[string]bool, len(in.Order))
		for i, value := range in.Order {
			in.Order[i], ok = validCatalogID(value)
			if !ok || ids[in.Order[i]] {
				return in, ErrInvalid
			}
			ids[in.Order[i]] = true
		}
	case "delete":
		if in.ExpectedRevision == 0 || in.Title != "" || in.ItemID != "" || len(in.Order) != 0 {
			return in, ErrInvalid
		}
	default:
		return in, ErrInvalid
	}
	return in, nil
}

func personalCommandFingerprint(in PersonalCollectionCommand) string {
	data, _ := json.Marshal(in)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// The event record and collection update are committed in the same serialized
// transaction. A duplicate is checked before revision or publication state so
// a lost acknowledgement can be safely retried even after later changes.
func (s *Service) ApplyPersonalCollection(ctx context.Context, key string, input PersonalCollectionCommand) (out PersonalCollectionAck, err error) {
	in, err := normalizePersonalCommand(input)
	if err != nil {
		return out, err
	}
	fingerprint := personalCommandFingerprint(in)
	err = s.Store.Update(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		eventKey := learningKey(client.ID, in.EventID)
		prior, e := recordAs[personalCollectionEvent](tx, "personal_collection_event", eventKey)
		if e == nil {
			if prior.Fingerprint != fingerprint {
				return ErrConflict
			}
			out = PersonalCollectionAck{EventID: in.EventID, Duplicate: true, Collection: prior.Collection}
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		collectionKey := learningKey(client.ID, in.CollectionID)
		collection, e := recordAs[PersonalCollection](tx, "personal_collection", collectionKey)
		if in.Action == "create" {
			if e == nil {
				return ErrConflict
			}
			if !errors.Is(e, ErrNotFound) {
				return e
			}
			now := s.Now().UTC()
			collection = PersonalCollection{ID: in.CollectionID, ClientID: client.ID, Title: in.Title,
				Revision: 1, Items: []string{}, CreatedAt: now, UpdatedAt: now}
		} else {
			if e != nil {
				return e
			}
			if collection.ClientID != client.ID || collection.ID != in.CollectionID || collection.DeletedAt != nil {
				return ErrNotFound
			}
			if collection.Revision != in.ExpectedRevision {
				return ErrConflict
			}
			changed := false
			switch in.Action {
			case "rename":
				if collection.Title != in.Title {
					collection.Title = in.Title
					changed = true
				}
			case "add":
				present := false
				for _, id := range collection.Items {
					if id == in.ItemID {
						present = true
						break
					}
				}
				if !present {
					if len(collection.Items) >= 1000 {
						return ErrInvalid
					}
					item, itemErr := tx.Item(in.ItemID)
					if itemErr != nil {
						return itemErr
					}
					if item.DeletedAt != nil || item.Published == nil {
						return ErrNotFound
					}
					collection.Items = append(collection.Items, in.ItemID)
					changed = true
				}
			case "remove":
				for i, id := range collection.Items {
					if id == in.ItemID {
						collection.Items = append(collection.Items[:i], collection.Items[i+1:]...)
						changed = true
						break
					}
				}
			case "reorder":
				if len(in.Order) != len(collection.Items) {
					return ErrConflict
				}
				current := make(map[string]bool, len(collection.Items))
				for _, id := range collection.Items {
					current[id] = true
				}
				for i, id := range in.Order {
					if !current[id] {
						return ErrConflict
					}
					if collection.Items[i] != id {
						changed = true
					}
				}
				if changed {
					collection.Items = append([]string{}, in.Order...)
				}
			case "delete":
				now := s.Now().UTC()
				collection.DeletedAt = &now
				changed = true
			}
			if changed {
				collection.Revision++
				collection.UpdatedAt = s.Now().UTC()
			}
		}
		if e = recordSave(tx, "personal_collection", collectionKey, collection); e != nil {
			return e
		}
		if e = recordSave(tx, "personal_collection_event", eventKey, personalCollectionEvent{Fingerprint: fingerprint, Collection: collection}); e != nil {
			return e
		}
		out = PersonalCollectionAck{EventID: in.EventID, Collection: collection}
		return nil
	})
	return out, err
}

func personalCollectionView(tx Tx, value PersonalCollection) (out PersonalCollectionView, err error) {
	out = PersonalCollectionView{ID: value.ID, ClientID: value.ClientID, Title: value.Title, Revision: value.Revision,
		Items: make([]PersonalCollectionItem, 0, len(value.Items)), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	for _, id := range value.Items {
		item, e := tx.Item(id)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return out, e
		}
		out.Items = append(out.Items, PersonalCollectionItem{ItemID: id, Available: e == nil && item.DeletedAt == nil && item.Published != nil})
	}
	return out, nil
}

func (s *Service) PersonalCollections(ctx context.Context, key string, limit, offset int) (out PersonalCollectionPage, err error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return out, ErrInvalid
	}
	out = PersonalCollectionPage{Items: []PersonalCollectionView{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		out.ClientID = client.ID
		all, e := recordsAs[PersonalCollection](tx, "personal_collection")
		if e != nil {
			return e
		}
		selected := make([]PersonalCollection, 0)
		for _, v := range all {
			if v.ClientID == client.ID && v.DeletedAt == nil {
				selected = append(selected, v)
			}
		}
		sort.Slice(selected, func(i, j int) bool {
			if selected[i].UpdatedAt.Equal(selected[j].UpdatedAt) {
				return selected[i].ID > selected[j].ID
			}
			return selected[i].UpdatedAt.After(selected[j].UpdatedAt)
		})
		out.Total = len(selected)
		if offset < len(selected) {
			for _, v := range selected[offset:min(offset+limit, len(selected))] {
				view, e := personalCollectionView(tx, v)
				if e != nil {
					return e
				}
				out.Items = append(out.Items, view)
			}
		}
		return nil
	})
	return out, err
}

func (s *Service) PersonalCollection(ctx context.Context, key, id string) (out PersonalCollectionView, err error) {
	id, ok := validCatalogID(id)
	if !ok {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		value, e := recordAs[PersonalCollection](tx, "personal_collection", learningKey(client.ID, id))
		if e != nil {
			return e
		}
		if value.ClientID != client.ID || value.ID != id || value.DeletedAt != nil {
			return ErrNotFound
		}
		out, e = personalCollectionView(tx, value)
		return e
	})
	return out, err
}
