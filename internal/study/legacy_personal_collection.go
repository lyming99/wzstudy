package study

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// LegacyPersonalCollectionInput is the already-split subset for one source and
// one authenticated Client. Ownership must be attested by the user; the server
// never deletes or changes the local collection.
type LegacyPersonalCollectionInput struct {
	EventID      string     `json:"event_id"`
	CollectionID string     `json:"collection_id"`
	Title        string     `json:"title"`
	Items        []string   `json:"items"`
	CreatedAt    *time.Time `json:"created_at"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

// ImportLegacyPersonalCollection creates one complete source-local collection
// atomically, never overwriting any live or deleted collection ID. Idempotency
// records share the same transaction as the collection and publication checks.
func (s *Service) ImportLegacyPersonalCollection(ctx context.Context, key string, input LegacyPersonalCollectionInput) (out PersonalCollectionAck, err error) {
	var ok bool
	input.EventID, ok = validCatalogID(input.EventID)
	if !ok {
		return out, ErrInvalid
	}
	input.CollectionID, ok = validCatalogID(input.CollectionID)
	if !ok {
		return out, ErrInvalid
	}
	if input.Title == "" || input.Title != strings.TrimSpace(input.Title) || !validText(input.Title, 500) ||
		len(input.Items) < 1 || len(input.Items) > 1000 || input.CreatedAt == nil || input.UpdatedAt == nil {
		return out, ErrInvalid
	}
	created, updated := input.CreatedAt.UTC().Round(0), input.UpdatedAt.UTC().Round(0)
	if created.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || updated.Before(created) || updated.After(s.Now().UTC().Add(5*time.Minute)) {
		return out, ErrInvalid
	}
	input.CreatedAt = &created
	input.UpdatedAt = &updated
	input.Items = append([]string(nil), input.Items...)
	seen := make(map[string]bool, len(input.Items))
	for i, id := range input.Items {
		input.Items[i], ok = validCatalogID(id)
		if !ok || seen[input.Items[i]] {
			return out, ErrInvalid
		}
		seen[input.Items[i]] = true
	}
	bytes, _ := json.Marshal(struct {
		Kind  string                        `json:"kind"`
		Input LegacyPersonalCollectionInput `json:"input"`
	}{"legacy_personal_collection_v1", input})
	hash := sha256.Sum256(bytes)
	fingerprint := hex.EncodeToString(hash[:])
	err = s.Store.Update(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		eventKey := learningKey(client.ID, input.EventID)
		prior, e := recordAs[personalCollectionEvent](tx, "personal_collection_event", eventKey)
		if e == nil {
			if prior.Fingerprint != fingerprint {
				return ErrConflict
			}
			out = PersonalCollectionAck{EventID: input.EventID, Duplicate: true, Collection: prior.Collection}
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		collectionKey := learningKey(client.ID, input.CollectionID)
		if _, e = tx.Record("personal_collection", collectionKey); e == nil {
			return ErrConflict
		} else if !errors.Is(e, ErrNotFound) {
			return e
		}
		for _, id := range input.Items {
			item, e := tx.Item(id)
			if e != nil {
				return e
			}
			if item.DeletedAt != nil || item.Published == nil {
				return ErrNotFound
			}
		}
		collection := PersonalCollection{ID: input.CollectionID, ClientID: client.ID, Title: input.Title, Revision: 1,
			Items: append([]string(nil), input.Items...), CreatedAt: created, UpdatedAt: updated}
		if e = recordSave(tx, "personal_collection", collectionKey, collection); e != nil {
			return e
		}
		if e = recordSave(tx, "personal_collection_event", eventKey, personalCollectionEvent{Fingerprint: fingerprint, Collection: collection}); e != nil {
			return e
		}
		out = PersonalCollectionAck{EventID: input.EventID, Collection: collection}
		return nil
	})
	return
}
