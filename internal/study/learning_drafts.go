package study

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

// LearningDraft is private to the authenticated Client. A cleared draft remains
// a tombstone with a revision so stale writes cannot silently resurrect it.
type LearningDraft struct {
	ClientID    string            `json:"client_id"`
	ItemID      string            `json:"item_id"`
	ParentID    string            `json:"parent_id,omitempty"`
	Text        string            `json:"text"`
	Position    *LearningPosition `json:"position,omitempty"`
	Revision    int64             `json:"revision"`
	UpdatedAt   time.Time         `json:"updated_at"`
	LastEventID string            `json:"last_event_id"`
	Deleted     bool              `json:"deleted"`
}
type LearningDraftCommand struct {
	EventID          string            `json:"event_id"`
	ItemID           string            `json:"item_id"`
	ParentID         string            `json:"parent_id,omitempty"`
	Text             string            `json:"text"`
	Position         *LearningPosition `json:"position,omitempty"`
	ExpectedRevision int64             `json:"expected_revision"`
}
type LegacyDraftInput struct {
	EventID      string            `json:"event_id"`
	ItemID       string            `json:"item_id"`
	ParentID     string            `json:"parent_id,omitempty"`
	Text         string            `json:"text"`
	Position     *LearningPosition `json:"position,omitempty"`
	ItemRevision int64             `json:"item_revision"`
	UpdatedAt    *time.Time        `json:"updated_at"`
}
type LearningDraftRead struct {
	ClientID string         `json:"client_id"`
	ItemID   string         `json:"item_id"`
	ParentID string         `json:"parent_id,omitempty"`
	Draft    *LearningDraft `json:"draft"`
}
type LearningDraftAck struct {
	EventID   string        `json:"event_id"`
	Duplicate bool          `json:"duplicate"`
	Draft     LearningDraft `json:"draft"`
}
type learningDraftEvent struct {
	Fingerprint string        `json:"fingerprint"`
	Draft       LearningDraft `json:"draft"`
}

func learningDraftKey(client, item, parent string) string { return client + ":" + item + ":" + parent }
func normalizeDraftCommand(in LearningDraftCommand) (LearningDraftCommand, error) {
	var ok bool
	in.EventID, ok = validCatalogID(in.EventID)
	if !ok {
		return in, ErrInvalid
	}
	in.ItemID, ok = validCatalogID(in.ItemID)
	if !ok {
		return in, ErrInvalid
	}
	if in.ParentID != "" {
		in.ParentID, ok = validCatalogID(in.ParentID)
		if !ok {
			return in, ErrInvalid
		}
	}
	if in.ExpectedRevision < 0 || !utf8.ValidString(in.Text) || utf8.RuneCountInString(in.Text) > 300 || (in.Text == "" && in.Position != nil) {
		return in, ErrInvalid
	}
	return in, nil
}
func (s *Service) ReadLearningDraft(ctx context.Context, key, itemID, parentID string) (out LearningDraftRead, err error) {
	var ok bool
	itemID, ok = validCatalogID(itemID)
	if !ok {
		return out, ErrInvalid
	}
	if parentID != "" {
		parentID, ok = validCatalogID(parentID)
		if !ok {
			return out, ErrInvalid
		}
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		out = LearningDraftRead{ClientID: client.ID, ItemID: itemID, ParentID: parentID}
		value, e := recordAs[LearningDraft](tx, "learning_draft", learningDraftKey(client.ID, itemID, parentID))
		if errors.Is(e, ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		out.Draft = &value
		return nil
	})
	return
}
func (s *Service) ApplyLearningDraft(ctx context.Context, key string, input LearningDraftCommand) (LearningDraftAck, error) {
	return s.applyLearningDraft(ctx, key, input, nil, 0)
}

// ImportLegacyDraft is explicitly attested by the user. It never overwrites an
// existing live or deleted draft and preserves the original edit timestamp.
func (s *Service) ImportLegacyDraft(ctx context.Context, key string, input LegacyDraftInput) (LearningDraftAck, error) {
	if input.ItemRevision < 1 || input.UpdatedAt == nil || input.Text == "" {
		return LearningDraftAck{}, ErrInvalid
	}
	updated := input.UpdatedAt.UTC().Round(0)
	if updated.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || updated.After(s.Now().UTC().Add(5*time.Minute)) {
		return LearningDraftAck{}, ErrInvalid
	}
	command := LearningDraftCommand{EventID: input.EventID, ItemID: input.ItemID, ParentID: input.ParentID, Text: input.Text, Position: input.Position, ExpectedRevision: 0}
	return s.applyLearningDraft(ctx, key, command, &updated, input.ItemRevision)
}
func (s *Service) applyLearningDraft(ctx context.Context, key string, input LearningDraftCommand, legacyAt *time.Time, itemRevision int64) (out LearningDraftAck, err error) {
	in, err := normalizeDraftCommand(input)
	if err != nil {
		return out, err
	}
	if legacyAt != nil && (in.Text == "" || in.ExpectedRevision != 0 || itemRevision < 1) {
		return out, ErrInvalid
	}
	material, _ := json.Marshal(struct {
		Kind      string               `json:"kind"`
		Command   LearningDraftCommand `json:"command"`
		UpdatedAt *time.Time           `json:"legacy_updated_at,omitempty"`
		Revision  int64                `json:"legacy_item_revision,omitempty"`
	}{
		func() string {
			if legacyAt != nil {
				return "legacy_draft_v1"
			}
			return "draft_v1"
		}(), in, legacyAt, itemRevision})
	sum := sha256.Sum256(material)
	fingerprint := hex.EncodeToString(sum[:])
	err = s.Store.Update(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		eventKey := learningKey(client.ID, in.EventID)
		prior, e := recordAs[learningDraftEvent](tx, "learning_draft_event", eventKey)
		if e == nil {
			if prior.Fingerprint != fingerprint {
				return ErrConflict
			}
			out = LearningDraftAck{EventID: in.EventID, Duplicate: true, Draft: prior.Draft}
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		key := learningDraftKey(client.ID, in.ItemID, in.ParentID)
		previous, e := recordAs[LearningDraft](tx, "learning_draft", key)
		if errors.Is(e, ErrNotFound) {
			previous = LearningDraft{ClientID: client.ID, ItemID: in.ItemID, ParentID: in.ParentID}
		} else if e != nil {
			return e
		}
		if previous.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		if legacyAt != nil && previous.Revision != 0 {
			return ErrConflict
		}
		if in.Text != "" {
			item, e := tx.Item(in.ItemID)
			if e != nil {
				return e
			}
			if item.DeletedAt != nil || item.Published == nil {
				return ErrNotFound
			}
			if legacyAt != nil && item.Published.Revision != itemRevision {
				return ErrConflict
			}
			if in.Position != nil && !validLearningPosition(in.Position, item, "") {
				return ErrInvalid
			}
			if in.ParentID != "" {
				parent, e := recordAs[RemoteLearningRecord](tx, "learning_record", learningKey(client.ID, in.ParentID))
				if e != nil {
					return e
				}
				if parent.ItemID != in.ItemID || parent.DeletedAt != nil {
					return ErrInvalid
				}
			}
		}
		updated := s.Now().UTC()
		if legacyAt != nil {
			updated = *legacyAt
		}
		next := LearningDraft{ClientID: client.ID, ItemID: in.ItemID, ParentID: in.ParentID, Text: in.Text, Position: in.Position,
			Revision: previous.Revision + 1, UpdatedAt: updated, LastEventID: in.EventID, Deleted: in.Text == ""}
		if e = recordSave(tx, "learning_draft", key, next); e != nil {
			return e
		}
		if e = recordSave(tx, "learning_draft_event", eventKey, learningDraftEvent{Fingerprint: fingerprint, Draft: next}); e != nil {
			return e
		}
		out = LearningDraftAck{EventID: in.EventID, Draft: next}
		return nil
	})
	return
}
