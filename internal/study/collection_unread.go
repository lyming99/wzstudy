package study

import (
	"context"
	"time"
)

// CollectionUnreadCheck is an observation, never a reservation or promised run.
type CollectionUnreadCheck struct {
	CheckedAt   time.Time `json:"checked_at"`
	UnreadCount int       `json:"unread_count"`
	Operator    string    `json:"operator"`
	Threshold   int       `json:"threshold"`
	Matched     bool      `json:"matched"`
}

type CollectionPlanPreview struct {
	Times       []time.Time            `json:"times"`
	UnreadCheck *CollectionUnreadCheck `json:"unread_check,omitempty"`
}

func collectionUnreadMatches(count int, condition CollectionUnreadCondition) bool {
	if condition.Threshold == nil {
		return false
	}
	switch condition.Operator {
	case "lt":
		return count < *condition.Threshold
	case "lte":
		return count <= *condition.Threshold
	case "eq":
		return count == *condition.Threshold
	case "gte":
		return count >= *condition.Threshold
	case "gt":
		return count > *condition.Threshold
	default:
		return false
	}
}

// Reuse the random-learning unread rule, scoped to the plan owner's personal
// reading identity. Legacy client keys and other workspaces must not affect it.
func collectionUnreadCount(tx Tx, owner string) (int, error) {
	if owner == "" {
		return 0, ErrUnauthorized
	}
	if scoped, ok := tx.(*workspaceTx); ok && scoped.owner != owner {
		return 0, ErrUnauthorized
	}
	states := map[string]LearningState{}
	all, err := recordsAs[LearningState](tx, "learning_state")
	if err != nil {
		return 0, err
	}
	for _, state := range all {
		if state.ClientID == owner {
			states[state.ItemID] = state
		}
	}
	feedback := map[string]FeedbackState{}
	allFeedback, err := recordsAs[FeedbackState](tx, "feedback_state")
	if err != nil {
		return 0, err
	}
	for _, state := range allFeedback {
		if state.ClientID == owner {
			feedback[state.ItemID] = state
		}
	}
	count := 0
	for offset := 0; ; offset += 500 {
		// Non-admin Items uses the public projection, including a public item
		// that has a newer private revision. Do not count drafts or tombstones.
		items, err := tx.Items(Filter{OwnerID: owner, Limit: 500, Offset: offset})
		if err != nil {
			return 0, err
		}
		for _, item := range items {
			if learningItemUnread(states[item.ID], feedback[item.ID]) {
				count++
			}
		}
		if len(items) < 500 {
			return count, nil
		}
	}
}

func evaluateCollectionUnread(tx Tx, owner string, schedule CollectionSchedule, now time.Time) (*CollectionUnreadCheck, error) {
	if schedule.Kind != "unread_inventory" {
		return nil, nil
	}
	if schedule.UnreadCondition == nil || schedule.UnreadCondition.Threshold == nil {
		return nil, ErrInvalid
	}
	count, err := collectionUnreadCount(tx, owner)
	if err != nil {
		return nil, err
	}
	condition := *schedule.UnreadCondition
	return &CollectionUnreadCheck{CheckedAt: now.UTC(), UnreadCount: count, Operator: condition.Operator,
		Threshold: *condition.Threshold, Matched: collectionUnreadMatches(count, condition)}, nil
}

// Preview is read-only: it observes current inventory and returns future CHECK
// slots, not guaranteed execution times. No source/task or run is created.
func (s *Service) PreviewCollectionPlan(ctx context.Context, schedule CollectionSchedule, count int) (out CollectionPlanPreview, err error) {
	schedule, err = normalizeCollectionSchedule(schedule)
	if err != nil {
		return out, err
	}
	now := s.Now().UTC()
	out.Times, err = PreviewCollectionSchedule(schedule, now, count)
	if err != nil || schedule.Kind != "unread_inventory" {
		return out, err
	}
	if WorkspaceUserID(ctx) == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		out.UnreadCheck, e = evaluateCollectionUnread(tx, WorkspaceUserID(ctx), schedule, now)
		return e
	})
	return out, err
}
