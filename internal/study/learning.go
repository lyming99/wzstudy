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
	"unicode/utf16"
	"unicode/utf8"
)

// Learning events are separate from v1 feedback: old clients never receive new
// action names on their existing feedback cursor. All identities come from keys.
type LearningPosition struct {
	Kind    string `json:"kind"`
	Version int64  `json:"version"`
	TimeMS  int64  `json:"time_ms,omitempty"`
	BlockID string `json:"block_id,omitempty"`
	Offset  int    `json:"offset,omitempty"`
	ImageID string `json:"image_id,omitempty"`
}

type LearningEventInput struct {
	EventID          string            `json:"event_id"`
	ItemID           string            `json:"item_id"`
	Action           string            `json:"action"`
	OccurredAt       *time.Time        `json:"occurred_at,omitempty"`
	Value            *bool             `json:"value,omitempty"`
	Position         *LearningPosition `json:"position,omitempty"`
	Mode             string            `json:"mode,omitempty"`
	ExpectedRevision *int64            `json:"expected_revision,omitempty"`
	DraftRevision    *int64            `json:"draft_revision,omitempty"`
	RecordID         string            `json:"record_id,omitempty"`
	ParentID         string            `json:"parent_id,omitempty"`
	Text             string            `json:"text,omitempty"`
	AsDanmaku        bool              `json:"as_danmaku,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	SessionSequence  int64             `json:"session_sequence,omitempty"`
	StartAt          *time.Time        `json:"start_at,omitempty"`
	EndAt            *time.Time        `json:"end_at,omitempty"`
}

// LegacyRecordInput is an explicit, conditional one-way import of an existing
// private record. It cannot specify a Client identity or delete old data.
type LegacyRecordInput struct {
	EventID      string            `json:"event_id"`
	ItemID       string            `json:"item_id"`
	RecordID     string            `json:"record_id"`
	ParentID     string            `json:"parent_id,omitempty"`
	Text         string            `json:"text"`
	Position     *LearningPosition `json:"position,omitempty"`
	AsDanmaku    bool              `json:"as_danmaku,omitempty"`
	CreatedAt    *time.Time        `json:"created_at"`
	UpdatedAt    *time.Time        `json:"updated_at"`
	ItemRevision int64             `json:"item_revision"`
}

// LegacyReviewInput imports only a pending plan, never a fabricated review.
type LegacyReviewInput struct {
	EventID      string     `json:"event_id"`
	ItemID       string     `json:"item_id"`
	ItemRevision int64      `json:"item_revision"`
	OccurredAt   *time.Time `json:"occurred_at"`
}

type LearningEvent struct {
	LearningEventInput
	ClientID    string    `json:"client_id"`
	Sequence    int64     `json:"sequence"`
	ReceivedAt  time.Time `json:"received_at"`
	Fingerprint string    `json:"fingerprint"`
}

type LearningProgress struct {
	Position    LearningPosition `json:"position"`
	Revision    int64            `json:"revision"`
	UpdatedAt   time.Time        `json:"updated_at"`
	LastEventID string           `json:"last_event_id,omitempty"`
}

type LearningState struct {
	ClientID          string                      `json:"client_id"`
	ItemID            string                      `json:"item_id"`
	ReviewPending     bool                        `json:"review_pending"`
	ReviewRevision    int64                       `json:"review_revision,omitempty"`
	ReviewLastEventID string                      `json:"review_last_event_id,omitempty"`
	ReviewCount       int64                       `json:"review_count"`
	ViewCount         int64                       `json:"view_count"`
	RecordCount       int64                       `json:"record_count"`
	DurationMS        int64                       `json:"duration_ms"`
	LastViewAt        *time.Time                  `json:"last_view_at,omitempty"`
	LastReviewAt      *time.Time                  `json:"last_review_at,omitempty"`
	Progress          map[string]LearningProgress `json:"progress"`
	UpdatedAt         time.Time                   `json:"updated_at"`
}

type RemoteLearningRecord struct {
	ID        string            `json:"id"`
	ClientID  string            `json:"client_id"`
	ItemID    string            `json:"item_id"`
	Text      string            `json:"text"`
	ParentID  string            `json:"parent_id,omitempty"`
	Position  *LearningPosition `json:"position,omitempty"`
	AsDanmaku bool              `json:"as_danmaku"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt *time.Time        `json:"updated_at,omitempty"`
	DeletedAt *time.Time        `json:"deleted_at,omitempty"`
}

type LearningAck struct {
	EventID   string `json:"event_id"`
	Sequence  int64  `json:"sequence"`
	Duplicate bool   `json:"duplicate"`
}
type LearningResult struct {
	Accepted   int             `json:"accepted"`
	Duplicates int             `json:"duplicates"`
	Events     []LearningAck   `json:"events"`
	States     []LearningState `json:"states"`
}
type learningClock struct {
	ClientID string `json:"client_id"`
	Sequence int64  `json:"sequence"`
}
type learningSession struct {
	ClientID string `json:"client_id"`
	ID       string `json:"id"`
	Sequence int64  `json:"sequence"`
}
type learningInterval struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}
type learningIntervals struct {
	ClientID string             `json:"client_id"`
	ItemID   string             `json:"item_id"`
	Ranges   []learningInterval `json:"ranges"`
}
type LearningPage struct {
	ClientID      string          `json:"client_id"`
	Items         []LearningState `json:"items"`
	Total         int             `json:"total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
	HighWatermark int64           `json:"high_watermark"`
	DurationMS    int64           `json:"duration_ms"`
}
type LearningRecordPage struct {
	Items  []RemoteLearningRecord `json:"items"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}
type LearningEventPage struct {
	Events        []LearningEvent `json:"events"`
	NextCursor    int64           `json:"next_cursor"`
	HighWatermark int64           `json:"high_watermark"`
	HasMore       bool            `json:"has_more"`
}

func learningKey(client, id string) string { return client + ":" + id }

func normalizeLearningEvent(in LearningEventInput, now time.Time) (LearningEventInput, error) {
	var ok bool
	in.EventID, ok = validCatalogID(in.EventID)
	if !ok {
		return in, ErrInvalid
	}
	in.ItemID, ok = validCatalogID(in.ItemID)
	if !ok {
		return in, ErrInvalid
	}
	if in.OccurredAt != nil {
		at := in.OccurredAt.UTC().Round(0)
		if at.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || at.After(now.Add(5*time.Minute)) {
			return in, ErrInvalid
		}
		in.OccurredAt = &at
	}
	if in.DraftRevision != nil && (in.Action != "record_create" || *in.DraftRevision < 1) {
		return in, ErrInvalid
	}
	noRecord := in.RecordID == "" && in.ParentID == "" && in.Text == "" && !in.AsDanmaku
	noSession := in.SessionID == "" && in.SessionSequence == 0 && in.StartAt == nil && in.EndAt == nil
	noProgress := in.Position == nil && in.Mode == "" && in.ExpectedRevision == nil
	switch in.Action {
	case "view", "review":
		if in.Value != nil || !noRecord || !noSession || !noProgress {
			return in, ErrInvalid
		}
	case "review_plan":
		if in.Value == nil || !noRecord || !noSession || !noProgress {
			return in, ErrInvalid
		}
	case "progress":
		if in.Value != nil || !noRecord || !noSession || in.Position == nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 || (in.Mode != "" && in.Mode != "preview" && in.Mode != "fulltext") {
			return in, ErrInvalid
		}
	case "record_create":
		if in.Value != nil || !noSession || in.ExpectedRevision != nil || in.Mode != "" {
			return in, ErrInvalid
		}
		in.RecordID, ok = validCatalogID(in.RecordID)
		if !ok {
			return in, ErrInvalid
		}
		if in.ParentID != "" {
			in.ParentID, ok = validCatalogID(in.ParentID)
			if !ok || in.ParentID == in.RecordID {
				return in, ErrInvalid
			}
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" || !validText(in.Text, 4000) || utf8.RuneCountInString(in.Text) > 300 {
			return in, ErrInvalid
		}
	case "record_delete":
		if in.Value != nil || !noSession || !noProgress || in.Text != "" || in.ParentID != "" || in.AsDanmaku {
			return in, ErrInvalid
		}
		in.RecordID, ok = validCatalogID(in.RecordID)
		if !ok {
			return in, ErrInvalid
		}
	case "duration":
		if in.Value != nil || !noRecord || !noProgress || in.StartAt == nil || in.EndAt == nil || in.SessionSequence < 1 {
			return in, ErrInvalid
		}
		in.SessionID, ok = validCatalogID(in.SessionID)
		if !ok {
			return in, ErrInvalid
		}
		start, end := in.StartAt.UTC().Round(0), in.EndAt.UTC().Round(0)
		if start.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || !end.After(start) || end.Sub(start) > 2*time.Minute || end.After(now.Add(5*time.Minute)) {
			return in, ErrInvalid
		}
		in.StartAt, in.EndAt = &start, &end
	default:
		return in, ErrInvalid
	}
	return in, nil
}

func learningFingerprint(in LearningEventInput) string {
	b, _ := json.Marshal(in)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validLearningPosition(pos *LearningPosition, item Item, mode string) bool {
	if pos == nil || item.Published == nil || pos.Version != item.Published.Revision || pos.Version < 1 || pos.TimeMS < 0 || pos.Offset < 0 {
		return false
	}
	content := item.Published.Content
	kind := Kind(content)
	// Link-only media are represented as article previews by WenzFlow.
	if kind != "article" && len(content.Assets) == 0 {
		kind = "article"
	}
	switch pos.Kind {
	case "media":
		return mode == "" && (kind == "video" || kind == "audio") && pos.BlockID == "" && pos.ImageID == "" && pos.Offset == 0 && pos.TimeMS <= int64((7*24*time.Hour)/time.Millisecond)
	case "article":
		if kind != "article" || pos.TimeMS != 0 || pos.ImageID != "" || (pos.BlockID != "body" && pos.BlockID != "source") {
			return false
		}
		body := content.Content
		if body == "" {
			body = content.Summary
		}
		if Kind(content) != "article" && len(content.Assets) == 0 {
			body += "\n\n该资料仅登记了外部链接，未上传可直接播放的媒体。"
		}
		if strings.TrimSpace(body) == "" {
			body = "该资料暂未提供正文。"
		}
		if pos.BlockID == "source" {
			if content.URL == "" {
				return false
			}
			body = "打开原始链接"
		}
		return pos.Offset <= len(utf16.Encode([]rune(body)))
	case "image":
		if mode != "" || kind != "carousel" || pos.TimeMS != 0 || pos.BlockID != "" || pos.Offset != 0 {
			return false
		}
		for _, asset := range content.Assets {
			if asset.ID == pos.ImageID {
				return true
			}
		}
	}
	return false
}

func learningStateFor(tx Tx, clientID, itemID string) (LearningState, error) {
	state, err := recordAs[LearningState](tx, "learning_state", learningKey(clientID, itemID))
	if errors.Is(err, ErrNotFound) {
		return LearningState{ClientID: clientID, ItemID: itemID, Progress: map[string]LearningProgress{}}, nil
	}
	if state.Progress == nil {
		state.Progress = map[string]LearningProgress{}
	}
	return state, err
}
func learningClockFor(tx Tx, clientID string) (learningClock, error) {
	clock, err := recordAs[learningClock](tx, "learning_clock", clientID)
	if errors.Is(err, ErrNotFound) {
		return learningClock{ClientID: clientID}, nil
	}
	return clock, err
}
func mergeLearningIntervals(intervals []learningInterval) []learningInterval {
	if len(intervals) == 0 {
		return []learningInterval{}
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].StartAt.Equal(intervals[j].StartAt) {
			return intervals[i].EndAt.Before(intervals[j].EndAt)
		}
		return intervals[i].StartAt.Before(intervals[j].StartAt)
	})
	out := []learningInterval{intervals[0]}
	for _, v := range intervals[1:] {
		last := &out[len(out)-1]
		if !v.StartAt.After(last.EndAt) {
			if v.EndAt.After(last.EndAt) {
				last.EndAt = v.EndAt
			}
			continue
		}
		out = append(out, v)
	}
	return out
}
func intervalMillis(intervals []learningInterval) int64 {
	var total int64
	for _, v := range intervals {
		total += v.EndAt.Sub(v.StartAt).Milliseconds()
	}
	return total
}

// All events, session clocks, records and state updates are atomic. The event
// id is scoped to the authenticated client and is checked before item status.
func (s *Service) ReportLearning(ctx context.Context, key string, inputs []LearningEventInput) (LearningResult, error) {
	return s.reportLearning(ctx, key, inputs, nil, nil)
}

type legacyRecordMeta struct {
	Revision  int64     `json:"item_revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Explicit, conditional migration of private, non-deleted records. The event
// identity includes the historical update time and item revision. Callers must
// establish ownership and obtain user consent; no source-side cleanup occurs.
func (s *Service) ImportLegacyRecords(ctx context.Context, key string, records []LegacyRecordInput) (LearningResult, error) {
	if len(records) < 1 || len(records) > 100 {
		return LearningResult{}, ErrInvalid
	}
	now := s.Now().UTC()
	inputs := make([]LearningEventInput, 0, len(records))
	metas := make(map[string]legacyRecordMeta, len(records))
	for _, record := range records {
		if record.CreatedAt == nil || record.UpdatedAt == nil || record.ItemRevision < 1 || record.Text != strings.TrimSpace(record.Text) {
			return LearningResult{}, ErrInvalid
		}
		created, updated := record.CreatedAt.UTC().Round(0), record.UpdatedAt.UTC().Round(0)
		if updated.Before(created) || updated.After(now.Add(5*time.Minute)) {
			return LearningResult{}, ErrInvalid
		}
		input := LearningEventInput{EventID: record.EventID, ItemID: record.ItemID, Action: "record_create", RecordID: record.RecordID,
			ParentID: record.ParentID, Text: record.Text, Position: record.Position, AsDanmaku: record.AsDanmaku, OccurredAt: &created}
		input, err := normalizeLearningEvent(input, now)
		if err != nil {
			return LearningResult{}, err
		}
		meta := legacyRecordMeta{Revision: record.ItemRevision, UpdatedAt: updated}
		if prior, exists := metas[input.EventID]; exists && prior != meta {
			return LearningResult{}, ErrConflict
		}
		metas[input.EventID] = meta
		inputs = append(inputs, input)
	}
	return s.reportLearning(ctx, key, inputs, metas, nil)
}

// ImportLegacyReviews only creates a pending plan if no prior review or plan
// event exists for the authenticated Client and the item version still matches.
func (s *Service) ImportLegacyReviews(ctx context.Context, key string, reviews []LegacyReviewInput) (LearningResult, error) {
	if len(reviews) < 1 || len(reviews) > 100 {
		return LearningResult{}, ErrInvalid
	}
	now := s.Now().UTC()
	inputs := make([]LearningEventInput, 0, len(reviews))
	revisions := make(map[string]int64, len(reviews))
	for _, review := range reviews {
		if review.ItemRevision < 1 || review.OccurredAt == nil {
			return LearningResult{}, ErrInvalid
		}
		value := true
		input := LearningEventInput{EventID: review.EventID, ItemID: review.ItemID, Action: "review_plan", Value: &value, OccurredAt: review.OccurredAt}
		input, err := normalizeLearningEvent(input, now)
		if err != nil {
			return LearningResult{}, err
		}
		if prior, exists := revisions[input.EventID]; exists && prior != review.ItemRevision {
			return LearningResult{}, ErrConflict
		}
		revisions[input.EventID] = review.ItemRevision
		inputs = append(inputs, input)
	}
	return s.reportLearning(ctx, key, inputs, nil, revisions)
}

func (s *Service) reportLearning(ctx context.Context, key string, inputs []LearningEventInput, legacy map[string]legacyRecordMeta, legacyReviews map[string]int64) (out LearningResult, err error) {
	if len(inputs) < 1 || len(inputs) > 100 {
		return out, ErrInvalid
	}
	now := s.Now().UTC()
	normalized := make([]LearningEventInput, len(inputs))
	for i, in := range inputs {
		normalized[i], err = normalizeLearningEvent(in, now)
		if err != nil {
			return out, err
		}
	}
	out = LearningResult{Events: []LearningAck{}, States: []LearningState{}}
	err = s.Store.Update(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		clock, e := learningClockFor(tx, client.ID)
		if e != nil {
			return e
		}
		touched := map[string]bool{}
		reviewHistory := map[string]bool{}
		if len(legacyReviews) > 0 {
			events, scanErr := recordsAs[LearningEvent](tx, "learning_event")
			if scanErr != nil {
				return scanErr
			}
			for _, event := range events {
				if event.ClientID == client.ID && (event.Action == "review_plan" || event.Action == "review") {
					reviewHistory[event.ItemID] = true
				}
			}
		}
		for _, in := range normalized {
			eventKey := learningKey(client.ID, in.EventID)
			fingerprint := learningFingerprint(in)
			meta, importing := legacy[in.EventID]
			reviewRevision, importingReview := legacyReviews[in.EventID]
			if importing && importingReview {
				return ErrInvalid
			}
			if importingReview {
				b, _ := json.Marshal(struct {
					Kind     string             `json:"kind"`
					Event    LearningEventInput `json:"event"`
					Revision int64              `json:"item_revision"`
				}{"legacy_review_v1", in, reviewRevision})
				h := sha256.Sum256(b)
				fingerprint = hex.EncodeToString(h[:])
			}
			if importing {
				b, _ := json.Marshal(struct {
					Kind  string             `json:"kind"`
					Event LearningEventInput `json:"event"`
					Meta  legacyRecordMeta   `json:"meta"`
				}{"legacy_record_v1", in, meta})
				h := sha256.Sum256(b)
				fingerprint = hex.EncodeToString(h[:])
			}
			prior, priorErr := recordAs[LearningEvent](tx, "learning_event", eventKey)
			if priorErr == nil {
				if prior.Fingerprint != fingerprint {
					return ErrConflict
				}
				out.Duplicates++
				out.Events = append(out.Events, LearningAck{EventID: in.EventID, Sequence: prior.Sequence, Duplicate: true})
				touched[in.ItemID] = true
				continue
			}
			if !errors.Is(priorErr, ErrNotFound) {
				return priorErr
			}
			var item Item
			if in.Action != "record_delete" {
				item, e = tx.Item(in.ItemID)
				if e != nil {
					return e
				}
				if item.DeletedAt != nil || item.Published == nil {
					return ErrNotFound
				}
				if importing && (in.Action != "record_create" || item.Published.Revision != meta.Revision) {
					return ErrConflict
				}
				if importingReview && (in.Action != "review_plan" || item.Published.Revision != reviewRevision) {
					return ErrConflict
				}
			}
			state, e := learningStateFor(tx, client.ID, in.ItemID)
			if e != nil {
				return e
			}
			if importingReview && (state.ReviewPending || state.ReviewRevision > 0 || state.ReviewCount > 0 || state.LastReviewAt != nil || reviewHistory[in.ItemID]) {
				return ErrConflict
			}
			now = s.Now().UTC()
			switch in.Action {
			case "view":
				state.ViewCount++
				state.LastViewAt = &now
			case "review_plan":
				state.ReviewPending = *in.Value
				state.ReviewRevision++
				state.ReviewLastEventID = in.EventID
				reviewHistory[in.ItemID] = true
			case "review":
				state.ReviewCount++
				state.ReviewPending = false
				state.ReviewRevision++
				state.ReviewLastEventID = in.EventID
				state.LastReviewAt = &now
				reviewHistory[in.ItemID] = true
			case "progress":
				if !validLearningPosition(in.Position, item, in.Mode) {
					return ErrInvalid
				}
				old := state.Progress[in.Mode]
				if old.Revision != *in.ExpectedRevision {
					return ErrConflict
				}
				state.Progress[in.Mode] = LearningProgress{Position: *in.Position, Revision: old.Revision + 1, UpdatedAt: now, LastEventID: in.EventID}
			case "record_create":
				if in.Position != nil && !validLearningPosition(in.Position, item, "") {
					return ErrInvalid
				}
				if in.AsDanmaku && (Kind(item.Published.Content) != "audio" || in.Position == nil || in.Position.Kind != "media") {
					return ErrInvalid
				}
				recordKey := learningKey(client.ID, in.RecordID)
				if _, e = tx.Record("learning_record", recordKey); e == nil {
					return ErrConflict
				} else if !errors.Is(e, ErrNotFound) {
					return e
				}
				if in.ParentID != "" {
					parent, parentErr := recordAs[RemoteLearningRecord](tx, "learning_record", learningKey(client.ID, in.ParentID))
					if errors.Is(parentErr, ErrNotFound) {
						return ErrInvalid
					}
					if parentErr != nil {
						return parentErr
					}
					if parent.ItemID != in.ItemID || parent.DeletedAt != nil {
						return ErrInvalid
					}
				}
				if in.DraftRevision != nil {
					draftKey := learningDraftKey(client.ID, in.ItemID, in.ParentID)
					draft, draftErr := recordAs[LearningDraft](tx, "learning_draft", draftKey)
					if errors.Is(draftErr, ErrNotFound) {
						return ErrConflict
					}
					if draftErr != nil {
						return draftErr
					}
					positionMatches := (draft.Position == nil && in.Position == nil) || (draft.Position != nil && in.Position != nil && *draft.Position == *in.Position)
					if draft.Deleted || draft.Revision != *in.DraftRevision || strings.TrimSpace(draft.Text) != in.Text || !positionMatches {
						return ErrConflict
					}
					draft.Text = ""
					draft.Position = nil
					draft.Deleted = true
					draft.Revision++
					draft.UpdatedAt = now
					draft.LastEventID = in.EventID
					if e = recordSave(tx, "learning_draft", draftKey, draft); e != nil {
						return e
					}
				}
				createdAt := now
				var updatedAt *time.Time
				if importing {
					createdAt = *in.OccurredAt
					updated := meta.UpdatedAt
					updatedAt = &updated
				}
				record := RemoteLearningRecord{ID: in.RecordID, ClientID: client.ID, ItemID: in.ItemID, Text: in.Text, ParentID: in.ParentID, Position: in.Position, AsDanmaku: in.AsDanmaku, CreatedAt: createdAt, UpdatedAt: updatedAt}
				if e = recordSave(tx, "learning_record", recordKey, record); e != nil {
					return e
				}
				state.RecordCount++
			case "record_delete":
				recordKey := learningKey(client.ID, in.RecordID)
				record, recordErr := recordAs[RemoteLearningRecord](tx, "learning_record", recordKey)
				if recordErr != nil {
					return recordErr
				}
				if record.ItemID != in.ItemID {
					return ErrNotFound
				}
				if record.DeletedAt == nil {
					record.DeletedAt = &now
					if e = recordSave(tx, "learning_record", recordKey, record); e != nil {
						return e
					}
					if state.RecordCount > 0 {
						state.RecordCount--
					}
				}
			case "duration":
				sessionKey := learningKey(client.ID, in.SessionID)
				session, sessionErr := recordAs[learningSession](tx, "learning_session", sessionKey)
				if errors.Is(sessionErr, ErrNotFound) {
					session = learningSession{ClientID: client.ID, ID: in.SessionID}
				} else if sessionErr != nil {
					return sessionErr
				}
				if in.SessionSequence != session.Sequence+1 {
					return ErrConflict
				}
				session.Sequence = in.SessionSequence
				if e = recordSave(tx, "learning_session", sessionKey, session); e != nil {
					return e
				}
				intervalKey := learningKey(client.ID, in.ItemID)
				intervals, intervalErr := recordAs[learningIntervals](tx, "learning_intervals", intervalKey)
				if errors.Is(intervalErr, ErrNotFound) {
					intervals = learningIntervals{ClientID: client.ID, ItemID: in.ItemID}
				} else if intervalErr != nil {
					return intervalErr
				}
				intervals.Ranges = mergeLearningIntervals(append(intervals.Ranges, learningInterval{StartAt: *in.StartAt, EndAt: *in.EndAt}))
				if len(intervals.Ranges) > 10000 {
					return ErrInvalid
				}
				state.DurationMS = intervalMillis(intervals.Ranges)
				if e = recordSave(tx, "learning_intervals", intervalKey, intervals); e != nil {
					return e
				}
			}
			state.UpdatedAt = now
			clock.Sequence++
			event := LearningEvent{LearningEventInput: in, ClientID: client.ID, Sequence: clock.Sequence, ReceivedAt: now, Fingerprint: fingerprint}
			if e = recordSave(tx, "learning_event", eventKey, event); e != nil {
				return e
			}
			if e = recordSave(tx, "learning_state", learningKey(client.ID, in.ItemID), state); e != nil {
				return e
			}
			out.Accepted++
			out.Events = append(out.Events, LearningAck{EventID: in.EventID, Sequence: clock.Sequence})
			touched[in.ItemID] = true
		}
		ids := make([]string, 0, len(touched))
		for id := range touched {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			state, e := learningStateFor(tx, client.ID, id)
			if e != nil {
				return e
			}
			out.States = append(out.States, state)
		}
		return recordSave(tx, "learning_clock", client.ID, clock)
	})
	if err != nil {
		out = LearningResult{}
	}
	return
}

func learningClientID(tx Tx, key, adminClientID string) (string, error) {
	if adminClientID != "" {
		id, ok := validCatalogID(adminClientID)
		if !ok {
			return "", ErrInvalid
		}
		if err := personalOwnerExists(tx, id); err != nil {
			return "", err
		}
		return id, nil
	}
	client, err := clientAccess(tx, key, PermissionFeedback)
	if err != nil {
		return "", err
	}
	return client.ID, nil
}

func (s *Service) ClientLearning(ctx context.Context, key, adminClientID string, itemIDs []string, limit, offset int) (LearningPage, error) {
	return s.ClientLearningPeriod(ctx, key, adminClientID, itemIDs, limit, offset, nil, nil)
}

// Period bounds are UTC instants calculated by the caller for its local day;
// using instants rather than a fixed offset also handles DST transitions.
func (s *Service) ClientLearningPeriod(ctx context.Context, key, adminClientID string, itemIDs []string, limit, offset int, from, until *time.Time) (out LearningPage, err error) {
	if (from == nil) != (until == nil) || (from != nil && (!until.After(*from) || until.Sub(*from) > 32*24*time.Hour)) {
		return out, ErrInvalid
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 || len(itemIDs) > 100 {
		return out, ErrInvalid
	}
	ids := map[string]bool{}
	for _, raw := range itemIDs {
		id, ok := validCatalogID(raw)
		if !ok {
			return out, ErrInvalid
		}
		ids[id] = true
	}
	out = LearningPage{Items: []LearningState{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		id, e := learningClientID(tx, key, adminClientID)
		if e != nil {
			return e
		}
		out.ClientID = id
		clock, e := learningClockFor(tx, id)
		if e != nil {
			return e
		}
		out.HighWatermark = clock.Sequence
		all, e := recordsAs[LearningState](tx, "learning_state")
		if e != nil {
			return e
		}
		filtered := []LearningState{}
		for _, state := range all {
			if state.ClientID == id && (len(ids) == 0 || ids[state.ItemID]) {
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
		if offset < len(filtered) {
			out.Items = filtered[offset:min(offset+limit, len(filtered))]
		}
		intervals, e := recordsAs[learningIntervals](tx, "learning_intervals")
		if e != nil {
			return e
		}
		ranges := []learningInterval{}
		for _, group := range intervals {
			if group.ClientID != id || (len(ids) != 0 && !ids[group.ItemID]) {
				continue
			}
			for _, interval := range group.Ranges {
				if from != nil {
					if !interval.EndAt.After(*from) || !interval.StartAt.Before(*until) {
						continue
					}
					if interval.StartAt.Before(*from) {
						interval.StartAt = *from
					}
					if interval.EndAt.After(*until) {
						interval.EndAt = *until
					}
				}
				ranges = append(ranges, interval)
			}
		}
		out.DurationMS = intervalMillis(mergeLearningIntervals(ranges))
		return nil
	})
	return
}

// ClientLearningRecord reads only the authenticated learner's exact record.
// It supports migration conflict checks without enumerating all private rows.
func (s *Service) ClientLearningRecord(ctx context.Context, key, itemID, recordID string) (out RemoteLearningRecord, err error) {
	itemID, ok := validCatalogID(itemID)
	if !ok {
		return out, ErrInvalid
	}
	recordID, ok = validCatalogID(recordID)
	if !ok {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionFeedback)
		if e != nil {
			return e
		}
		record, e := recordAs[RemoteLearningRecord](tx, "learning_record", learningKey(client.ID, recordID))
		if e != nil {
			return e
		}
		if record.ItemID != itemID || record.DeletedAt != nil {
			return ErrNotFound
		}
		out = record
		return nil
	})
	return
}

func (s *Service) ClientLearningRecords(ctx context.Context, key, adminClientID, itemID string, limit, offset int) (out LearningRecordPage, err error) {
	itemID, ok := validCatalogID(itemID)
	if !ok || limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return out, ErrInvalid
	}
	out = LearningRecordPage{Items: []RemoteLearningRecord{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		id, e := learningClientID(tx, key, adminClientID)
		if e != nil {
			return e
		}
		all, e := recordsAs[RemoteLearningRecord](tx, "learning_record")
		if e != nil {
			return e
		}
		filtered := []RemoteLearningRecord{}
		for _, record := range all {
			if record.ClientID == id && record.ItemID == itemID && record.DeletedAt == nil {
				filtered = append(filtered, record)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
				return filtered[i].ID > filtered[j].ID
			}
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		})
		out.Total = len(filtered)
		if offset < len(filtered) {
			out.Items = filtered[offset:min(offset+limit, len(filtered))]
		}
		return nil
	})
	return
}

// LearningItemStats contains aggregate counts only; no client identities or
// private records are returned. The item must still be published.
type LearningItemStats struct {
	ItemID        string `json:"item_id"`
	LikeCount     int64  `json:"like_count"`
	FavoriteCount int64  `json:"favorite_count"`
	ViewCount     int64  `json:"view_count"`
	ReviewCount   int64  `json:"review_count"`
	RecordCount   int64  `json:"record_count"`
}

func (s *Service) LearningStats(ctx context.Context, itemID string) (out LearningItemStats, err error) {
	itemID, ok := validCatalogID(itemID)
	if !ok {
		return out, ErrInvalid
	}
	out.ItemID = itemID
	err = s.Store.View(ctx, func(tx Tx) error {
		item, e := tx.Item(itemID)
		if e != nil {
			return e
		}
		if item.DeletedAt != nil || item.Published == nil {
			return ErrNotFound
		}
		feedback, e := recordsAs[FeedbackState](tx, "feedback_state")
		if e != nil {
			return e
		}
		for _, state := range feedback {
			if state.ItemID != itemID {
				continue
			}
			if state.Actions["like"].Active {
				out.LikeCount++
			}
			if state.Actions["favorite"].Active {
				out.FavoriteCount++
			}
		}
		states, e := recordsAs[LearningState](tx, "learning_state")
		if e != nil {
			return e
		}
		for _, state := range states {
			if state.ItemID == itemID {
				out.ViewCount += state.ViewCount
				out.ReviewCount += state.ReviewCount
				out.RecordCount += state.RecordCount
			}
		}
		return nil
	})
	return
}

func (s *Service) ClientLearningEvents(ctx context.Context, key, adminClientID string, after int64, limit int) (out LearningEventPage, err error) {
	if after < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	out = LearningEventPage{Events: []LearningEvent{}, NextCursor: after}
	err = s.Store.View(ctx, func(tx Tx) error {
		id, e := learningClientID(tx, key, adminClientID)
		if e != nil {
			return e
		}
		clock, e := learningClockFor(tx, id)
		if e != nil {
			return e
		}
		out.HighWatermark = clock.Sequence
		if after > clock.Sequence {
			return ErrInvalid
		}
		all, e := recordsAs[LearningEvent](tx, "learning_event")
		if e != nil {
			return e
		}
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
