package study

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	roadmapKind     = "learning_roadmap"
	RoadmapMaxNodes = 300
	RoadmapMaxDepth = 12 // The root is level 1.
)

// RoadmapNode is a branch of an administrator-authored roadmap, not a learner's
// progress. Slice order defines sibling order; nodes need not arrive preorder.
type RoadmapNode struct {
	ID        string   `json:"id"`
	ParentID  string   `json:"parent_id"`
	Title     string   `json:"title"`
	Note      string   `json:"note"`
	ItemIDs   []string `json:"item_ids"`
	Collapsed bool     `json:"collapsed"`
}

type LearningRoadmap struct {
	ID        string        `json:"id"`
	Revision  int64         `json:"revision"`
	Title     string        `json:"title"`
	Summary   string        `json:"summary"`
	Nodes     []RoadmapNode `json:"nodes"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	DeletedAt *time.Time    `json:"deleted_at,omitempty"`
}

type RoadmapInput struct {
	Revision int64         `json:"revision"`
	Title    string        `json:"title"`
	Summary  string        `json:"summary"`
	Nodes    []RoadmapNode `json:"nodes"`
}

// List responses intentionally omit the potentially large node descriptions.
type RoadmapSummary struct {
	ID            string    `json:"id"`
	Revision      int64     `json:"revision"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary"`
	NodeCount     int       `json:"node_count"`
	ResourceCount int       `json:"resource_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RoadmapPage struct {
	Items  []RoadmapSummary `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

func roadmapText(value string, max int) bool {
	return validText(value, max*4) && utf8.RuneCountInString(value) <= max
}

func learningRoadmap(tx Tx, id string) (LearningRoadmap, error) {
	id, ok := validCatalogID(id)
	if !ok {
		return LearningRoadmap{}, ErrInvalid
	}
	out, err := recordAs[LearningRoadmap](tx, roadmapKind, id)
	if err != nil {
		return LearningRoadmap{}, err
	}
	if out.DeletedAt != nil {
		return LearningRoadmap{}, ErrNotFound
	}
	return out, nil
}

func normalizeRoadmap(tx Tx, in RoadmapInput, previous LearningRoadmap) (RoadmapInput, error) {
	in.Title, in.Summary = strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary)
	if in.Title == "" || !roadmapText(in.Title, 200) || !roadmapText(in.Summary, 2000) || len(in.Nodes) < 1 || len(in.Nodes) > RoadmapMaxNodes {
		return RoadmapInput{}, ErrInvalid
	}
	// A deleted resource may remain attached to its original node, but cannot be
	// attached anew. This preserves existing maps without making them uneditable.
	oldLinks := map[string]map[string]bool{}
	for _, node := range previous.Nodes {
		oldLinks[node.ID] = map[string]bool{}
		for _, id := range node.ItemIDs {
			oldLinks[node.ID][id] = true
		}
	}
	out := in
	out.Nodes = make([]RoadmapNode, len(in.Nodes))
	byID := map[string]int{}
	roots := 0
	rootIndex := 0
	for i, node := range in.Nodes {
		var ok bool
		node.ID, ok = validCatalogID(node.ID)
		if !ok {
			return RoadmapInput{}, ErrInvalid
		}
		if _, duplicate := byID[node.ID]; duplicate {
			return RoadmapInput{}, ErrInvalid
		}
		byID[node.ID] = i
		if node.ParentID == "" {
			roots++
			rootIndex = i
		} else {
			node.ParentID, ok = validCatalogID(node.ParentID)
			if !ok || node.ParentID == node.ID {
				return RoadmapInput{}, ErrInvalid
			}
		}
		node.Title, node.Note = strings.TrimSpace(node.Title), strings.TrimSpace(node.Note)
		if node.Title == "" || !roadmapText(node.Title, 200) || !roadmapText(node.Note, 4000) || len(node.ItemIDs) > 20 {
			return RoadmapInput{}, ErrInvalid
		}
		seen := map[string]bool{}
		items := make([]string, 0, len(node.ItemIDs))
		for _, rawID := range node.ItemIDs {
			id, valid := validCatalogID(rawID)
			if !valid || seen[id] {
				return RoadmapInput{}, ErrInvalid
			}
			seen[id] = true
			item, err := tx.Item(id)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return RoadmapInput{}, err
			}
			if (errors.Is(err, ErrNotFound) || item.DeletedAt != nil) && !oldLinks[node.ID][id] {
				return RoadmapInput{}, ErrInvalid
			}
			items = append(items, id)
		}
		node.ItemIDs = items
		out.Nodes[i] = node
	}
	if roots != 1 || out.Nodes[rootIndex].Title != out.Title {
		return RoadmapInput{}, ErrInvalid
	}
	// Each chain must terminate at the sole root within the depth limit. This
	// simultaneously rejects missing parents, cycles and disconnected subgraphs.
	for _, node := range out.Nodes {
		parent, depth := node.ParentID, 1
		for parent != "" {
			index, exists := byID[parent]
			if !exists {
				return RoadmapInput{}, ErrInvalid
			}
			depth++
			if depth > RoadmapMaxDepth {
				return RoadmapInput{}, ErrInvalid
			}
			parent = out.Nodes[index].ParentID
		}
	}
	return out, nil
}

func (s *Service) SaveLearningRoadmap(ctx context.Context, id string, in RoadmapInput) (out LearningRoadmap, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		now := s.Now().UTC()
		if id == "" {
			if in.Revision != 0 {
				return ErrInvalid
			}
			out = LearningRoadmap{ID: uuid.NewString(), CreatedAt: now}
		} else {
			var e error
			out, e = learningRoadmap(tx, id)
			if e != nil {
				return e
			}
			if in.Revision != out.Revision {
				return ErrConflict
			}
		}
		normalized, e := normalizeRoadmap(tx, in, out)
		if e != nil {
			return e
		}
		out.Title, out.Summary, out.Nodes = normalized.Title, normalized.Summary, normalized.Nodes
		out.Revision++
		out.UpdatedAt = now
		return recordSave(tx, roadmapKind, out.ID, out)
	})
	if err != nil {
		out = LearningRoadmap{}
	}
	return
}

func (s *Service) LearningRoadmap(ctx context.Context, id string) (out LearningRoadmap, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		out, err = learningRoadmap(tx, id)
		return err
	})
	return
}

func (s *Service) LearningRoadmaps(ctx context.Context, q string, limit, offset int) (out RoadmapPage, err error) {
	q = strings.TrimSpace(q)
	if !roadmapText(q, 200) || limit < 1 || limit > 50 || offset < 0 || offset > 10000 {
		return out, ErrInvalid
	}
	out = RoadmapPage{Items: []RoadmapSummary{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[LearningRoadmap](tx, roadmapKind)
		if e != nil {
			return e
		}
		filtered := []RoadmapSummary{}
		for _, v := range all {
			if v.DeletedAt != nil || (q != "" && !strings.Contains(strings.ToLower(v.Title+"\n"+v.Summary), strings.ToLower(q))) {
				continue
			}
			resources := map[string]bool{}
			for _, node := range v.Nodes {
				for _, id := range node.ItemIDs {
					resources[id] = true
				}
			}
			filtered = append(filtered, RoadmapSummary{ID: v.ID, Revision: v.Revision, Title: v.Title, Summary: v.Summary, NodeCount: len(v.Nodes), ResourceCount: len(resources), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
				return filtered[i].ID > filtered[j].ID
			}
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		})
		out.Total = len(filtered)
		if offset < len(filtered) {
			out.Items = filtered[offset:min(offset+limit, len(filtered))]
		}
		return nil
	})
	return
}

func (s *Service) DeleteLearningRoadmap(ctx context.Context, id string, revision int64) error {
	return s.Store.Update(ctx, func(tx Tx) error {
		out, err := learningRoadmap(tx, id)
		if err != nil {
			return err
		}
		if revision != out.Revision {
			return ErrConflict
		}
		now := s.Now().UTC()
		out.Revision++
		out.UpdatedAt, out.DeletedAt = now, &now
		return recordSave(tx, roadmapKind, out.ID, out)
	})
}
