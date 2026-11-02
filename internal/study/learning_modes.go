package study

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mode choices are public projections of this workspace, never personal local
// collections. Collection chapters and roadmap nodes remain managed by the
// existing revision-checked workspace editors.
type LearningModeChoice struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Count   int    `json:"count"`
}
type LearningModeChoices struct {
	Items  []LearningModeChoice `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}
type LearningModeFilter struct {
	Mode, Category, CollectionID, RoadmapID, NodeID, Query string
	Seed                                                   int64
	Limit, Offset                                          int
}
type LearningModePage struct {
	UnreadItemIDs []string      `json:"unread_item_ids"`
	Items         []*PublicItem `json:"items"`
	Total         int           `json:"total"`
	Limit         int           `json:"limit"`
	Offset        int           `json:"offset"`
	HasMore       bool          `json:"has_more"`
}

func publicModeItems(tx Tx, query string) ([]Item, error) {
	out := []Item{}
	for offset := 0; ; offset += 500 {
		page, err := tx.Items(Filter{Query: query, Limit: 500, Offset: offset})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 500 {
			return out, nil
		}
	}
}

// Unread means never started, not merely unfinished; revisions do not reset it.
func learningItemUnread(state LearningState, feedback FeedbackState) bool {
	return state.ViewCount == 0 && len(state.Progress) == 0 && !feedback.Actions["read"].Active
}

func modePageValid(mode string, limit, offset int) bool {
	return (mode == "news" || mode == "collection" || mode == "route" || mode == "review" || mode == "random") && limit >= 1 && limit <= 100 && offset >= 0 && offset <= 1000000
}

func (s *Service) LearningModeCategories(ctx context.Context, key, mode string, limit, offset int) (out LearningModeChoices, err error) {
	if !modePageValid(mode, limit, offset) {
		return out, ErrInvalid
	}
	out = LearningModeChoices{Items: []LearningModeChoice{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		if _, e := clientAccess(tx, key, PermissionRead, PermissionSync); e != nil {
			return e
		}
		choices := []LearningModeChoice{}
		switch mode {
		case "news":
			items, e := publicModeItems(tx, "")
			if e != nil {
				return e
			}
			count := 0
			for _, item := range items {
				if newsContent(item.Published.Content) {
					count++
				}
			}
			choices = append(choices, LearningModeChoice{ID: "@uncategorized", Title: "资讯内容", Count: count})
		case "collection":
			all, e := recordsAs[CatalogCollection](tx, "catalog_collection")
			if e != nil {
				return e
			}
			for _, v := range all {
				if v.DeletedAt != nil || v.Published == nil {
					continue
				}
				visible, e := catalogVisible(tx, *v.Published)
				if e != nil {
					return e
				}
				choices = append(choices, LearningModeChoice{ID: v.ID, Title: visible.Title, Summary: visible.Summary, Count: len(visible.Chapters)})
			}
		case "route":
			all, e := recordsAs[LearningRoadmap](tx, roadmapKind)
			if e != nil {
				return e
			}
			for _, v := range all {
				if v.DeletedAt != nil {
					continue
				}
				ids, e := visibleRoadmapItems(tx, v, "")
				if e != nil {
					return e
				}
				choices = append(choices, LearningModeChoice{ID: v.ID, Title: v.Title, Summary: v.Summary, Count: len(ids)})
			}
		}
		sort.Slice(choices, func(i, j int) bool {
			if choices[i].Title == choices[j].Title {
				return choices[i].ID < choices[j].ID
			}
			return choices[i].Title < choices[j].Title
		})
		out.Total = len(choices)
		if offset < len(choices) {
			out.Items = choices[offset:min(offset+limit, len(choices))]
		}
		return nil
	})
	return
}

// Traverse parents before children in sibling order, ignoring UI folding. A
// published projection never exposes the bodies or titles of unpublished items.
func visibleRoadmapItems(tx Tx, roadmap LearningRoadmap, nodeID string) ([]string, error) {
	children := map[string][]RoadmapNode{}
	root := ""
	for _, node := range roadmap.Nodes {
		children[node.ParentID] = append(children[node.ParentID], node)
		if node.ParentID == "" {
			root = node.ID
		}
	}
	if nodeID != "" {
		found := false
		for _, node := range roadmap.Nodes {
			if node.ID == nodeID {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
		root = nodeID
	}
	ids, seen := []string{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		for _, node := range roadmap.Nodes {
			if node.ID == id {
				for _, itemID := range node.ItemIDs {
					item, e := tx.Item(itemID)
					if errors.Is(e, ErrNotFound) {
						continue
					}
					if e != nil {
						return e
					}
					if item.DeletedAt == nil && item.Published != nil && !seen[itemID] {
						seen[itemID] = true
						ids = append(ids, itemID)
					}
				}
				break
			}
		}
		for _, child := range children[id] {
			if e := visit(child.ID); e != nil {
				return e
			}
		}
		return nil
	}
	if e := visit(root); e != nil {
		return nil, e
	}
	return ids, nil
}

func reviewDue(state LearningState, now time.Time) bool {
	return (state.ReviewActive || state.ReviewPending) && (state.NextReviewAt == nil || !state.NextReviewAt.After(now))
}

func (s *Service) LearningModeItems(ctx context.Context, key string, f LearningModeFilter) (out LearningModePage, err error) {
	if !modePageValid(f.Mode, f.Limit, f.Offset) || !validText(f.Query, 600) || !validText(f.Category, 200) || f.Seed < 0 || (f.CollectionID != "" && f.Mode != "collection") || (f.RoadmapID != "" && f.Mode != "route") || (f.NodeID != "" && f.Mode != "route") {
		return out, ErrInvalid
	}
	if f.Mode == "collection" {
		if _, ok := validCatalogID(f.CollectionID); !ok {
			return out, ErrInvalid
		}
	}
	if f.Mode == "route" {
		if _, ok := validCatalogID(f.RoadmapID); !ok {
			return out, ErrInvalid
		}
	}
	if f.NodeID != "" {
		id, ok := validCatalogID(f.NodeID)
		if !ok {
			return out, ErrInvalid
		}
		f.NodeID = id
	}
	out = LearningModePage{Items: []*PublicItem{}, Limit: f.Limit, Offset: f.Offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		client, e := clientAccess(tx, key, PermissionRead, PermissionSync)
		if e != nil {
			return e
		}
		if f.Mode == "review" && !client.Allows(PermissionFeedback) {
			return ErrForbidden
		}
		items, e := publicModeItems(tx, f.Query)
		if e != nil {
			return e
		}
		byID := map[string]Item{}
		for _, item := range items {
			byID[item.ID] = item
		}
		ids := []string{}
		unreadIDs := map[string]bool{}
		switch f.Mode {
		case "collection":
			v, e := catalogCollection(tx, f.CollectionID)
			if e != nil {
				return e
			}
			if v.Published == nil {
				return ErrNotFound
			}
			for _, chapter := range v.Published.Chapters {
				if _, ok := byID[chapter.ItemID]; ok {
					ids = append(ids, chapter.ItemID)
				}
			}
		case "route":
			v, e := learningRoadmap(tx, f.RoadmapID)
			if e != nil {
				return e
			}
			ordered, e := visibleRoadmapItems(tx, v, f.NodeID)
			if e != nil {
				return e
			}
			for _, id := range ordered {
				if _, ok := byID[id]; ok {
					ids = append(ids, id)
				}
			}
		default:
			states := map[string]LearningState{}
			feedback := map[string]FeedbackState{}
			if client.Allows(PermissionFeedback) {
				all, e := recordsAs[LearningState](tx, "learning_state")
				if e != nil {
					return e
				}
				for _, state := range all {
					if state.ClientID == client.ID {
						states[state.ItemID] = state
					}
				}
				allFeedback, e := recordsAs[FeedbackState](tx, "feedback_state")
				if e != nil {
					return e
				}
				for _, state := range allFeedback {
					if state.ClientID == client.ID {
						feedback[state.ItemID] = state
					}
				}
			}
			for _, item := range items {
				// News uses independent content mode, never the topic category. A stale category parameter
				// from an older client must not narrow it to a selected topic.
				if f.Mode == "news" && !newsContent(item.Published.Content) {
					continue
				}
				if f.Mode == "review" && !reviewDue(states[item.ID], s.Now()) {
					continue
				}
				ids = append(ids, item.ID)
			}
			if f.Mode == "review" {
				sort.SliceStable(ids, func(i, j int) bool {
					a, b := states[ids[i]].NextReviewAt, states[ids[j]].NextReviewAt
					if a == nil {
						return b != nil
					}
					if b == nil {
						return false
					}
					return a.Before(*b)
				})
			}
			if f.Mode == "random" {
				unread := func(id string) bool {
					return learningItemUnread(states[id], feedback[id])
				}
				for _, id := range ids {
					if unread(id) {
						unreadIDs[id] = true
					}
				}
				rank := func(id string) string {
					sum := sha256.Sum256([]byte(strconv.FormatInt(f.Seed, 10) + ":" + id))
					return hex.EncodeToString(sum[:])
				}
				sort.Slice(ids, func(i, j int) bool {
					a, b := unread(ids[i]), unread(ids[j])
					if a != b {
						return a
					}
					return rank(ids[i]) < rank(ids[j])
				})
				// Keep one seed's order stable across view reports and page reloads. The
				// bounded, short-lived snapshot contains IDs only and is identity-scoped.
				sum := sha256.Sum256([]byte(WorkspaceUserID(ctx) + ":" + client.ID + ":" + strconv.FormatInt(f.Seed, 10) + ":" + f.Query))
				sessionKey := "mode:" + hex.EncodeToString(sum[:])
				s.recommendMu.Lock()
				if s.recommendations == nil {
					s.recommendations = map[string]recommendationSession{}
				}
				if prior, ok := s.recommendations[sessionKey]; ok && prior.ExpiresAt.After(s.Now()) {
					ids = append([]string{}, prior.ItemIDs...)
					unreadIDs = map[string]bool{}
					for _, id := range prior.UnreadIDs {
						unreadIDs[id] = true
					}
				} else {
					for id, v := range s.recommendations {
						if !v.ExpiresAt.After(s.Now()) {
							delete(s.recommendations, id)
						}
					}
					if len(s.recommendations) >= recommendationSessionLimit {
						s.recommendMu.Unlock()
						return ErrBusy
					}
					unreadSnapshot := []string{}
					for _, id := range ids {
						if unreadIDs[id] {
							unreadSnapshot = append(unreadSnapshot, id)
						}
					}
					s.recommendations[sessionKey] = recommendationSession{UnreadIDs: unreadSnapshot, ID: sessionKey, ClientID: client.ID, ItemIDs: append([]string{}, ids...), ExpiresAt: s.Now().Add(recommendationTTL)}
				}
				s.recommendMu.Unlock()
			}
		}
		visible := []*PublicItem{}
		for _, id := range ids {
			if item, ok := byID[id]; ok {
				visible = append(visible, item.Published)
			}
		}
		out.Total = len(visible)
		if f.Offset < len(visible) {
			out.Items = visible[f.Offset:min(f.Offset+f.Limit, len(visible))]
		}
		out.UnreadItemIDs = []string{}
		for _, item := range out.Items {
			if unreadIDs[item.ID] {
				out.UnreadItemIDs = append(out.UnreadItemIDs, item.ID)
			}
		}
		out.HasMore = f.Offset+len(out.Items) < out.Total
		return nil
	})
	return
}

// These intervals are product defaults, not a claim of a medically optimal
// schedule. Users may replace them with their own positive minute intervals.
var defaultReviewIntervals = []int{20, 60, 540, 1440, 2880, 5760, 10080, 21600, 43200}

func validReviewIntervals(intervals []int) bool {
	if len(intervals) > 30 {
		return false
	}
	for _, n := range intervals {
		if n < 1 || n > 525600 {
			return false
		}
	}
	return true
}
func scheduleReview(state *LearningState, at time.Time) {
	intervals := state.ReviewIntervalsMinutes
	if len(intervals) == 0 {
		intervals = append([]int{}, defaultReviewIntervals...)
		state.ReviewIntervalsMinutes = intervals
	}
	index := min(state.ReviewStep, len(intervals)-1)
	next := at.Add(time.Duration(intervals[index]) * time.Minute)
	state.NextReviewAt = &next
	state.ReviewStep++
}

// Normalize display names at ingestion rather than exposing the collection
// system's name as the original content platform.
func contentPlatform(content Content, fallback string) string {
	if strings.TrimSpace(content.Platform) != "" {
		return strings.TrimSpace(content.Platform)
	}
	uri, _ := url.Parse(content.URL)
	host := ""
	if uri != nil {
		host = strings.ToLower(uri.Hostname())
	}
	for _, v := range []struct{ host, name string }{{"bilibili.com", "哔哩哔哩"}, {"b23.tv", "哔哩哔哩"}, {"youtube.com", "YouTube"}, {"youtu.be", "YouTube"}, {"douyin.com", "抖音"}, {"xiaohongshu.com", "小红书"}, {"weixin.qq.com", "微信公众号"}, {"zhihu.com", "知乎"}} {
		if host == v.host || strings.HasSuffix(host, "."+v.host) {
			return v.name
		}
	}
	if host != "" {
		return strings.TrimPrefix(host, "www.")
	}
	return strings.TrimSpace(fallback)
}
