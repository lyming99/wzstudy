package study

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const recommendationCandidateLimit = 1000
const recommendationSessionLimit = 256
const recommendationTTL = 10 * time.Minute

type recommendationSession struct {
	ID           string
	ClientID     string
	Personalized bool
	ItemIDs      []string
	ExpiresAt    time.Time
}
type RecommendationPage struct {
	Items      []*PublicItem `json:"items"`
	NextCursor string        `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
	ExpiresAt  time.Time     `json:"expires_at"`
}
type recommendationCandidate struct {
	item  Item
	score int
}

func recommendationCursor(id string, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id + ":" + strconv.Itoa(offset)))
}
func parseRecommendationCursor(cursor string) (string, int, error) {
	if len(cursor) > 160 {
		return "", 0, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, ErrInvalid
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		return "", 0, ErrInvalid
	}
	id, ok := validCatalogID(parts[0])
	if !ok {
		return "", 0, ErrInvalid
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 || offset > recommendationCandidateLimit {
		return "", 0, ErrInvalid
	}
	return id, offset, nil
}

// Recommendation sessions hold only ordered public item IDs in bounded memory.
// They are short lived, bound to the authenticated client, and never produce
// feedback or advance a persistent cursor merely because a query was made.
func (s *Service) Recommendations(ctx context.Context, key, cursor string, limit int) (out RecommendationPage, err error) {
	if limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	var session recommendationSession
	offset := 0
	if cursor == "" {
		var client Client
		var candidates []recommendationCandidate
		err = s.Store.View(ctx, func(tx Tx) error {
			var e error
			client, e = clientAccess(tx, key, PermissionRead)
			if e != nil {
				return e
			}
			items, e := tx.Items(Filter{Limit: recommendationCandidateLimit})
			if e != nil {
				return e
			}
			feedbackByItem := map[string]FeedbackState{}
			learningByItem := map[string]LearningState{}
			if client.Allows(PermissionFeedback) {
				feedback, readErr := recordsAs[FeedbackState](tx, "feedback_state")
				if readErr != nil {
					return readErr
				}
				for _, v := range feedback {
					if v.ClientID == client.ID {
						feedbackByItem[v.ItemID] = v
					}
				}
				learning, readErr := recordsAs[LearningState](tx, "learning_state")
				if readErr != nil {
					return readErr
				}
				for _, v := range learning {
					if v.ClientID == client.ID {
						learningByItem[v.ItemID] = v
					}
				}
			}
			tags, categories := map[string]int{}, map[string]int{}
			for _, item := range items {
				if item.Published == nil {
					continue
				}
				state := feedbackByItem[item.ID]
				if state.Actions["like"].Active || state.Actions["favorite"].Active {
					for _, tag := range item.Published.Tags {
						tags[tag]++
					}
					if item.Published.Category != "" {
						categories[item.Published.Category]++
					}
				}
			}
			for _, item := range items {
				if item.Published == nil || item.DeletedAt != nil {
					continue
				}
				feedback, learning := feedbackByItem[item.ID], learningByItem[item.ID]
				score := 0
				if learning.ReviewPending {
					score += 1000
				}
				if learning.ViewCount == 0 {
					score += 100
				} else {
					score -= int(min(learning.ViewCount, 25)) * 2
				}
				if feedback.Actions["read"].Active {
					score -= 20
				}
				if feedback.Actions["recommended"].Count > 0 {
					score -= 15
				}
				if feedback.Actions["favorite"].Active {
					score += 15
				}
				if feedback.Actions["like"].Active {
					score += 10
				}
				for _, tag := range item.Published.Tags {
					score += min(tags[tag], 3) * 6
				}
				score += min(categories[item.Published.Category], 3) * 5
				candidates = append(candidates, recommendationCandidate{item: item, score: score})
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			a, b := itemDate(candidates[i].item), itemDate(candidates[j].item)
			if a.Equal(b) {
				return candidates[i].item.ID > candidates[j].item.ID
			}
			return a.After(b)
		})
		session = recommendationSession{ID: uuid.NewString(), ClientID: client.ID, Personalized: client.Allows(PermissionFeedback), ItemIDs: make([]string, 0, len(candidates)), ExpiresAt: s.Now().UTC().Add(recommendationTTL)}
		for _, candidate := range candidates {
			session.ItemIDs = append(session.ItemIDs, candidate.item.ID)
		}
		s.recommendMu.Lock()
		if s.recommendations == nil {
			s.recommendations = map[string]recommendationSession{}
		}
		now := s.Now().UTC()
		for id, existing := range s.recommendations {
			if !now.Before(existing.ExpiresAt) {
				delete(s.recommendations, id)
			}
		}
		if len(s.recommendations) >= recommendationSessionLimit {
			oldestID := ""
			var oldest time.Time
			for id, existing := range s.recommendations {
				if oldestID == "" || existing.ExpiresAt.Before(oldest) || (existing.ExpiresAt.Equal(oldest) && id < oldestID) {
					oldestID, oldest = id, existing.ExpiresAt
				}
			}
			delete(s.recommendations, oldestID)
		}
		s.recommendations[session.ID] = session
		s.recommendMu.Unlock()
	} else {
		var id string
		id, offset, err = parseRecommendationCursor(cursor)
		if err != nil {
			return out, err
		}
		s.recommendMu.Lock()
		var exists bool
		session, exists = s.recommendations[id]
		if exists && !s.Now().UTC().Before(session.ExpiresAt) {
			delete(s.recommendations, id)
			exists = false
		}
		s.recommendMu.Unlock()
		if !exists {
			return out, ErrNotFound
		}
	}
	if offset > len(session.ItemIDs) {
		return out, ErrInvalid
	}
	out = RecommendationPage{Items: []*PublicItem{}, ExpiresAt: session.ExpiresAt}
	err = s.Store.View(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionRead)
		if e != nil {
			return e
		}
		if client.ID != session.ClientID {
			return ErrNotFound
		}
		if client.Allows(PermissionFeedback) != session.Personalized {
			return ErrConflict
		}
		for offset < len(session.ItemIDs) && len(out.Items) < limit {
			id := session.ItemIDs[offset]
			offset++
			item, itemErr := tx.Item(id)
			if errors.Is(itemErr, ErrNotFound) {
				continue
			}
			if itemErr != nil {
				return itemErr
			}
			if item.DeletedAt == nil && item.Published != nil {
				out.Items = append(out.Items, item.Published)
			}
		}
		return nil
	})
	if err != nil {
		return RecommendationPage{}, err
	}
	out.HasMore = offset < len(session.ItemIDs)
	if out.HasMore {
		out.NextCursor = recommendationCursor(session.ID, offset)
	}
	return
}
