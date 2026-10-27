package study

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var FeedbackActions = []string{"recommended", "impression", "read", "like", "record", "favorite"}
var FeedbackExclusions = []string{"recommended", "seen", "read", "liked", "recorded", "favorited"}

type FeedbackInput struct {
	EventID      string     `json:"event_id"`
	ItemID       string     `json:"item_id"`
	Action       string     `json:"action"`
	Value        *bool      `json:"value,omitempty"`
	ItemRevision int64      `json:"item_revision,omitempty"`
	OccurredAt   *time.Time `json:"occurred_at,omitempty"`
	Note         string     `json:"note,omitempty"`
	Progress     *float64   `json:"progress,omitempty"`
}

type FeedbackEvent struct {
	FeedbackInput
	ClientID       string    `json:"client_id"`
	Sequence       int64     `json:"sequence"`
	EffectiveAt    time.Time `json:"effective_at"`
	ReceivedAt     time.Time `json:"received_at"`
	PublicRevision int64     `json:"public_revision"`
	Fingerprint    string    `json:"fingerprint"`
}

type FeedbackActionState struct {
	Active         bool       `json:"active"`
	Count          int64      `json:"count"`
	LastAt         *time.Time `json:"last_at,omitempty"`
	LastPositiveAt *time.Time `json:"last_positive_at,omitempty"`
	LastEventID    string     `json:"last_event_id,omitempty"`
	ItemRevision   int64      `json:"item_revision,omitempty"`
	Note           string     `json:"note,omitempty"`
	Progress       *float64   `json:"progress,omitempty"`
}

type FeedbackState struct {
	ClientID  string                         `json:"client_id"`
	ItemID    string                         `json:"item_id"`
	Actions   map[string]FeedbackActionState `json:"actions"`
	UpdatedAt time.Time                      `json:"updated_at"`
}

type feedbackClock struct {
	ClientID string           `json:"client_id"`
	Sequence int64            `json:"sequence"`
	Counts   map[string]int64 `json:"counts"`
}

type FeedbackAck struct {
	EventID   string `json:"event_id"`
	Sequence  int64  `json:"sequence"`
	Duplicate bool   `json:"duplicate"`
}

type FeedbackResult struct {
	Accepted   int             `json:"accepted"`
	Duplicates int             `json:"duplicates"`
	Events     []FeedbackAck   `json:"events"`
	States     []FeedbackState `json:"states"`
}

type FeedbackPage struct {
	ClientID      string           `json:"client_id"`
	Items         []FeedbackState  `json:"items"`
	Total         int              `json:"total"`
	Limit         int              `json:"limit"`
	Offset        int              `json:"offset"`
	Summary       map[string]int64 `json:"summary"`
	EventCounts   map[string]int64 `json:"event_counts"`
	HighWatermark int64            `json:"high_watermark"`
}

type FeedbackEventPage struct {
	Events        []FeedbackEvent `json:"events"`
	NextCursor    int64           `json:"next_cursor"`
	HighWatermark int64           `json:"high_watermark"`
	HasMore       bool            `json:"has_more"`
}

func feedbackKey(client, item string) string { return client + ":" + item }

func normalizeFeedback(v FeedbackInput, now time.Time) (FeedbackInput, error) {
	event, e1 := uuid.Parse(v.EventID)
	item, e2 := uuid.Parse(v.ItemID)
	if e1 != nil || e2 != nil || event == uuid.Nil || item == uuid.Nil || !slices.Contains(FeedbackActions, v.Action) || v.ItemRevision < 0 {
		return v, ErrInvalid
	}
	v.EventID, v.ItemID = event.String(), item.String()
	value := v.Value == nil || *v.Value
	v.Value = &value
	if !value && (v.Action == "recommended" || v.Action == "impression") {
		return v, ErrInvalid
	}
	if !validText(v.Note, 4000) || (v.Progress != nil && (math.IsNaN(*v.Progress) || math.IsInf(*v.Progress, 0) || *v.Progress < 0 || *v.Progress > 1)) {
		return v, ErrInvalid
	}
	if (v.Note != "" || v.Progress != nil) && (v.Action != "record" || !value) {
		return v, ErrInvalid
	}
	if v.OccurredAt != nil {
		at := v.OccurredAt.UTC().Round(0)
		if at.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || at.After(now.Add(5*time.Minute)) {
			return v, ErrInvalid
		}
		v.OccurredAt = &at
	}
	return v, nil
}

func feedbackFingerprint(v FeedbackInput) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func feedbackClockFor(tx Tx, clientID string) (feedbackClock, error) {
	clock, e := recordAs[feedbackClock](tx, "feedback_clock", clientID)
	if errors.Is(e, ErrNotFound) {
		return feedbackClock{ClientID: clientID, Counts: map[string]int64{}}, nil
	}
	if clock.Counts == nil {
		clock.Counts = map[string]int64{}
	}
	return clock, e
}
func feedbackStateFor(tx Tx, clientID, itemID string) (FeedbackState, error) {
	state, e := recordAs[FeedbackState](tx, "feedback_state", feedbackKey(clientID, itemID))
	if errors.Is(e, ErrNotFound) {
		return FeedbackState{ClientID: clientID, ItemID: itemID, Actions: map[string]FeedbackActionState{}}, nil
	}
	if state.Actions == nil {
		state.Actions = map[string]FeedbackActionState{}
	}
	return state, e
}

// ReportFeedback checks authorization again under the serialized write lock.
// The whole batch, idempotency records, counters and state changes commit together.
// Feedback never publishes content or appends to the content change cursor.
func (s *Service) ReportFeedback(ctx context.Context, key string, inputs []FeedbackInput) (FeedbackResult, error) {
	return s.reportFeedback(ctx, key, inputs, false)
}

// ImportLegacyFeedback is deliberately narrower than normal feedback: it may
// fill missing like/favorite state only, never overwrite an existing server
// choice. The condition, event idempotency and mutation share one transaction.
func (s *Service) ImportLegacyFeedback(ctx context.Context, key string, inputs []FeedbackInput) (FeedbackResult, error) {
	for _, input := range inputs {
		if (input.Action != "like" && input.Action != "favorite") || input.Value == nil || input.OccurredAt == nil || input.ItemRevision < 1 || input.Note != "" || input.Progress != nil {
			return FeedbackResult{}, ErrInvalid
		}
	}
	return s.reportFeedback(ctx, key, inputs, true)
}

func (s *Service) reportFeedback(ctx context.Context, key string, inputs []FeedbackInput, onlyAbsent bool) (out FeedbackResult, err error) {
	if len(inputs) < 1 || len(inputs) > 100 {
		return out, ErrInvalid
	}
	now := s.Now().UTC()
	normalized := make([]FeedbackInput, len(inputs))
	for i, v := range inputs {
		normalized[i], err = normalizeFeedback(v, now)
		if err != nil {
			return out, err
		}
	}
	out = FeedbackResult{Events: []FeedbackAck{}, States: []FeedbackState{}}
	err = s.Store.Update(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		now = s.Now().UTC() // receive/order timestamps are captured under the serialized lock
		clock, e := feedbackClockFor(tx, client.ID)
		if e != nil {
			return e
		}
		touched := map[string]bool{}
		for _, input := range normalized {
			id := feedbackKey(client.ID, input.EventID)
			fingerprint := feedbackFingerprint(input)
			prior, e := recordAs[FeedbackEvent](tx, "feedback_event", id)
			if e == nil {
				if prior.Fingerprint != fingerprint {
					return ErrConflict
				}
				out.Duplicates++
				out.Events = append(out.Events, FeedbackAck{input.EventID, prior.Sequence, true})
				touched[input.ItemID] = true
				continue
			}
			if !errors.Is(e, ErrNotFound) {
				return e
			}
			item, e := tx.Item(input.ItemID)
			if e != nil {
				return e
			}
			if item.DeletedAt != nil || item.Published == nil {
				return ErrNotFound
			}
			revision := input.ItemRevision
			if revision == 0 {
				revision = item.Published.Revision
			}
			if revision > item.Published.Revision {
				return ErrInvalid
			}
			if onlyAbsent {
				// Version equality avoids applying a historical action to changed
				// content. Even an inactive prior server state is a real choice.
				if revision != item.Published.Revision {
					return ErrConflict
				}
				current, stateErr := feedbackStateFor(tx, client.ID, input.ItemID)
				if stateErr != nil {
					return stateErr
				}
				if _, exists := current.Actions[input.Action]; exists {
					return ErrConflict
				}
			}
			clock.Sequence++
			clock.Counts[input.Action]++
			at := now
			if input.OccurredAt != nil {
				at = *input.OccurredAt
			}
			event := FeedbackEvent{FeedbackInput: input, ClientID: client.ID, Sequence: clock.Sequence, EffectiveAt: at, ReceivedAt: now, PublicRevision: revision, Fingerprint: fingerprint}
			state, e := feedbackStateFor(tx, client.ID, input.ItemID)
			if e != nil {
				return e
			}
			action := state.Actions[input.Action]
			if *input.Value {
				action.Count++
				if action.LastPositiveAt == nil || at.After(*action.LastPositiveAt) {
					positive := at
					action.LastPositiveAt = &positive
				}
			}
			if action.LastAt == nil || !at.Before(*action.LastAt) {
				action.Active = *input.Value
				latest := at
				action.LastAt = &latest
				action.LastEventID = input.EventID
				action.ItemRevision = revision
				action.Note = input.Note
				action.Progress = input.Progress
			}
			state.Actions[input.Action] = action
			state.UpdatedAt = now
			if e = recordSave(tx, "feedback_event", id, event); e != nil {
				return e
			}
			if e = recordSave(tx, "feedback_state", feedbackKey(client.ID, input.ItemID), state); e != nil {
				return e
			}
			out.Accepted++
			out.Events = append(out.Events, FeedbackAck{input.EventID, event.Sequence, false})
			touched[input.ItemID] = true
		}
		ids := []string{}
		for id := range touched {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			state, e := feedbackStateFor(tx, client.ID, id)
			if e != nil {
				return e
			}
			out.States = append(out.States, state)
		}
		return recordSave(tx, "feedback_clock", client.ID, clock)
	})
	if err != nil {
		out = FeedbackResult{}
	}
	return
}

func ValidateFeedbackExclusions(exclude []string) error {
	seen := map[string]bool{}
	for _, v := range exclude {
		if !slices.Contains(FeedbackExclusions, v) || seen[v] {
			return ErrInvalid
		}
		seen[v] = true
	}
	return nil
}

func feedbackExcluded(state FeedbackState, exclude []string, since *time.Time) bool {
	positive := func(a FeedbackActionState) bool {
		return a.Count > 0 && a.LastPositiveAt != nil && (since == nil || !a.LastPositiveAt.Before(*since))
	}
	for _, filter := range exclude {
		switch filter {
		case "recommended":
			if positive(state.Actions["recommended"]) {
				return true
			}
		case "seen":
			for _, a := range state.Actions {
				if positive(a) {
					return true
				}
			}
		default:
			action := map[string]string{"read": "read", "liked": "like", "recorded": "record", "favorited": "favorite"}[filter]
			a := state.Actions[action]
			if a.Active && (since == nil || a.LastAt != nil && !a.LastAt.Before(*since)) {
				return true
			}
		}
	}
	return false
}

func feedbackPage(tx Tx, clientID string, itemIDs []string, limit, offset int) (out FeedbackPage, err error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 || len(itemIDs) > 100 {
		return out, ErrInvalid
	}
	all, e := recordsAs[FeedbackState](tx, "feedback_state")
	if e != nil {
		return out, e
	}
	clock, e := feedbackClockFor(tx, clientID)
	if e != nil {
		return out, e
	}
	out = FeedbackPage{ClientID: clientID, Items: []FeedbackState{}, Limit: limit, Offset: offset, Summary: map[string]int64{}, EventCounts: clock.Counts, HighWatermark: clock.Sequence}
	filtered := []FeedbackState{}
	for _, state := range all {
		if state.ClientID != clientID {
			continue
		}
		out.Summary["items"]++
		for _, flag := range FeedbackExclusions {
			if feedbackExcluded(state, []string{flag}, nil) {
				out.Summary[flag]++
			}
		}
		if len(itemIDs) == 0 || slices.Contains(itemIDs, state.ItemID) {
			filtered = append(filtered, state)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].ItemID > filtered[j].ItemID
		}
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	out.Total = len(filtered)
	if offset < out.Total {
		out.Items = filtered[offset:min(offset+limit, out.Total)]
	}
	return out, nil
}

func (s *Service) ClientFeedback(ctx context.Context, key, adminClientID string, ids []string, limit, offset int) (out FeedbackPage, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		id := adminClientID
		if id == "" {
			client, e := clientAccess(tx, key, PermissionRead, PermissionSync, PermissionFeedback)
			if e != nil {
				return e
			}
			id = client.ID
		} else {
			if _, e := recordAs[clientRecord](tx, "client", id); e != nil {
				return e
			}
		}
		var e error
		out, e = feedbackPage(tx, id, ids, limit, offset)
		return e
	})
	return
}

func (s *Service) FeedbackEvents(ctx context.Context, key, adminClientID string, after int64, limit int) (out FeedbackEventPage, err error) {
	if after < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		id := adminClientID
		if id == "" {
			client, e := clientAccess(tx, key, PermissionRead, PermissionSync, PermissionFeedback)
			if e != nil {
				return e
			}
			id = client.ID
		} else {
			if _, e := recordAs[clientRecord](tx, "client", id); e != nil {
				return e
			}
		}
		clock, e := feedbackClockFor(tx, id)
		if e != nil {
			return e
		}
		if after > clock.Sequence {
			return ErrInvalid
		}
		all, e := recordsAs[FeedbackEvent](tx, "feedback_event")
		if e != nil {
			return e
		}
		out = FeedbackEventPage{Events: []FeedbackEvent{}, NextCursor: after, HighWatermark: clock.Sequence}
		for _, event := range all {
			if event.ClientID == id && event.Sequence > after {
				out.Events = append(out.Events, event)
			}
		}
		sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].Sequence < out.Events[j].Sequence })
		if len(out.Events) > limit {
			out.Events = out.Events[:limit]
		}
		if len(out.Events) > 0 {
			out.NextCursor = out.Events[len(out.Events)-1].Sequence
		}
		out.HasMore = out.NextCursor < out.HighWatermark
		return nil
	})
	return
}

type ClientItemPage struct {
	Items    []*PublicItem            `json:"items"`
	Feedback map[string]FeedbackState `json:"feedback,omitempty"`
	Limit    int                      `json:"limit"`
	Offset   int                      `json:"offset"`
	ClientID string                   `json:"client_id"`
}

func (s *Service) ClientItems(ctx context.Context, key, adminClientID string, f Filter, exclude []string, since *time.Time, include bool) (out ClientItemPage, err error) {
	if f.Admin || ValidateFeedbackExclusions(exclude) != nil || f.Limit < 1 || f.Limit > 200 || f.Offset < 0 || f.Offset > 10000 {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		id := adminClientID
		if id == "" {
			client, e := clientAccess(tx, key, PermissionRead)
			if e != nil {
				return e
			}
			id = client.ID
		} else {
			if _, e := recordAs[clientRecord](tx, "client", id); e != nil {
				return e
			}
		}
		f.FeedbackClientID = id
		f.FeedbackExclude = exclude
		f.FeedbackSince = since
		items, e := tx.Items(f)
		if e != nil {
			return e
		}
		out = ClientItemPage{Items: []*PublicItem{}, Limit: f.Limit, Offset: f.Offset, ClientID: id}
		if include {
			out.Feedback = map[string]FeedbackState{}
		}
		for _, item := range items {
			out.Items = append(out.Items, item.Published)
			if include {
				state, e := feedbackStateFor(tx, id, item.ID)
				if e != nil {
					return e
				}
				out.Feedback[item.ID] = state
			}
		}
		return nil
	})
	return
}

// SQL condition uses only fixed action names, never client-supplied SQL.
func feedbackSQLConditions(alias string, exclude []string, since string) string {
	positive := func(action string) string {
		path := alias + ".record->'actions'->'" + action + "'"
		s := "COALESCE((" + path + "->>'count')::bigint,0)>0"
		if since != "" {
			s += " AND (" + path + "->>'last_positive_at')::timestamptz >= " + since
		}
		return "(" + s + ")"
	}
	parts := []string{}
	for _, v := range exclude {
		switch v {
		case "recommended":
			parts = append(parts, positive("recommended"))
		case "seen":
			all := []string{}
			for _, a := range FeedbackActions {
				all = append(all, positive(a))
			}
			parts = append(parts, "("+strings.Join(all, " OR ")+")")
		default:
			action := map[string]string{"read": "read", "liked": "like", "recorded": "record", "favorited": "favorite"}[v]
			if action == "" {
				continue
			}
			path := alias + ".record->'actions'->'" + action + "'"
			s := "COALESCE((" + path + "->>'active')::boolean,false)"
			if since != "" {
				s += " AND (" + path + "->>'last_at')::timestamptz >= " + since
			}
			parts = append(parts, "("+s+")")
		}
	}
	return strings.Join(parts, " OR ")
}
