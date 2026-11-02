package study

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

type OrganizationItem struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type ItemOrganizationInput struct {
	Items       []OrganizationItem `json:"items"`
	Category    *string            `json:"category,omitempty"`
	ContentMode *string            `json:"content_mode,omitempty"`
	// nil leaves memberships unchanged; empty removes all memberships.
	CollectionID       *string `json:"collection_id,omitempty"`
	CollectionRevision int64   `json:"collection_revision,omitempty"`
}
type ItemOrganizationResult struct {
	Items              []OrganizationItem `json:"items"`
	ModeUpdated        int                `json:"mode_updated"`
	CategoryUpdated    int                `json:"category_updated"`
	Moved              int                `json:"moved"`
	CollectionsUpdated int                `json:"collections_updated"`
}

// Organizing never republishes draft bodies, replaces assets, changes item IDs
// or writes another learner's state. The whole selection commits atomically.
func (s *Service) OrganizeItems(ctx context.Context, in ItemOrganizationInput) (out ItemOrganizationResult, err error) {
	if len(in.Items) < 1 || len(in.Items) > 100 || (in.Category == nil && in.CollectionID == nil && in.ContentMode == nil) {
		return out, ErrInvalid
	}
	if in.Category != nil {
		value := strings.TrimSpace(*in.Category)
		if !validText(value, 200) {
			return out, ErrInvalid
		}
		in.Category = &value
	}
	if in.ContentMode != nil {
		value, e := normalizeContentMode(*in.ContentMode)
		if e != nil {
			return out, e
		}
		in.ContentMode = &value
	}
	target := ""
	if in.CollectionID != nil {
		target = strings.TrimSpace(*in.CollectionID)
		if target != "" {
			id, ok := validCatalogID(target)
			if !ok || in.CollectionRevision < 1 {
				return out, ErrInvalid
			}
			target = id
		} else if in.CollectionRevision != 0 {
			return out, ErrInvalid
		}
	} else if in.CollectionRevision != 0 {
		return out, ErrInvalid
	}
	seen := map[string]bool{}
	for i, item := range in.Items {
		id, ok := validCatalogID(item.ID)
		if !ok || seen[id] || item.Revision < 1 {
			return out, ErrInvalid
		}
		seen[id] = true
		in.Items[i].ID = id
	}
	out.Items = []OrganizationItem{}
	err = s.Store.Update(ctx, func(tx Tx) error {
		now := s.Now().UTC()
		items := make([]Item, 0, len(in.Items))
		for _, request := range in.Items {
			item, e := tx.Item(request.ID)
			if e != nil {
				return e
			}
			if item.DeletedAt != nil {
				return ErrNotFound
			}
			if item.Revision != request.Revision {
				return ErrConflict
			}
			items = append(items, item)
		}
		collections := []CatalogCollection{}
		targetIndex := -1
		if in.CollectionID != nil {
			var e error
			collections, e = recordsAs[CatalogCollection](tx, "catalog_collection")
			if e != nil {
				return e
			}
			for i, collection := range collections {
				if collection.ID == target && collection.DeletedAt == nil {
					if collection.Revision != in.CollectionRevision {
						return ErrConflict
					}
					targetIndex = i
				}
			}
			if target != "" && targetIndex < 0 {
				return ErrNotFound
			}
		}
		// Preflight target capacity before changing any content or membership.
		if targetIndex >= 0 {
			count := len(collections[targetIndex].Draft.Chapters)
			existing := map[string]bool{}
			for _, chapter := range collections[targetIndex].Draft.Chapters {
				existing[chapter.ItemID] = true
			}
			for _, item := range items {
				if !existing[item.ID] {
					count++
				}
			}
			if count > 100 {
				return ErrInvalid
			}
		}
		moved := map[string]bool{}
		if in.CollectionID != nil {
			for i, collection := range collections {
				if collection.DeletedAt != nil {
					continue
				}
				changed := false
				chapters := make([]CatalogChapter, 0, len(collection.Draft.Chapters))
				existing := map[string]bool{}
				for _, chapter := range collection.Draft.Chapters {
					if seen[chapter.ItemID] && collection.ID != target {
						changed = true
						moved[chapter.ItemID] = true
						continue
					}
					chapters = append(chapters, chapter)
					existing[chapter.ItemID] = true
				}
				if i == targetIndex {
					for _, item := range items {
						if existing[item.ID] {
							continue
						}
						title := item.Draft.Title
						if item.Published != nil {
							title = item.Published.Title
						}
						chapters = append(chapters, CatalogChapter{ID: uuid.NewString(), Title: organizationChapterTitle(title), ItemID: item.ID})
						changed = true
						moved[item.ID] = true
					}
				}
				if !changed {
					continue
				}
				collection.Draft.Chapters = chapters
				collection.Revision++
				collection.UpdatedAt = now
				if e := saveImmediateCatalog(tx, &collection); e != nil {
					return e
				}
				out.CollectionsUpdated++
			}
		}
		for _, item := range items {
			categoryChanged := in.Category != nil && (item.Draft.Category != *in.Category || (item.Published != nil && item.Published.Category != *in.Category))
			modeChanged := in.ContentMode != nil && (item.Draft.ContentMode != *in.ContentMode || (item.Published != nil && item.Published.ContentMode != *in.ContentMode))
			if categoryChanged || modeChanged {
				item.Status = ReviewState(item)
				if categoryChanged {
					item.Draft.Category = *in.Category
					out.CategoryUpdated++
				}
				if modeChanged {
					item.Draft.ContentMode = *in.ContentMode
					out.ModeUpdated++
				}
				item.ContentHash = digest(item.Draft)
				item.Revision++
				item.UpdatedAt = now
				audit(&item, "资料分类或内容模式已更新", now)
				if item.Published != nil {
					// Change only selected metadata. Pending title/body/assets
					// remain separate from the public snapshot.
					if categoryChanged {
						item.Published.Category = *in.Category
					}
					if modeChanged {
						item.Published.ContentMode = *in.ContentMode
					}
					item.Published.Revision = item.Revision
					item.Published.UpdatedAt = now
				}
				if e := tx.SaveItem(item); e != nil {
					return e
				}
				if item.Published != nil {
					if _, e := tx.Append(Change{Operation: "upsert", ItemID: item.ID, SourceID: item.SourceID, Revision: item.Published.Revision, Item: item.Published, CreatedAt: now}); e != nil {
						return e
					}
				}
			}
			out.Items = append(out.Items, OrganizationItem{ID: item.ID, Revision: item.Revision})
		}
		out.Moved = len(moved)
		return nil
	})
	if err != nil {
		out = ItemOrganizationResult{}
	}
	return
}

// Item titles may be 500 UTF-8 bytes, while chapter labels allow 200. A move
// must not create an invalid collection that its editor can no longer save.
func organizationChapterTitle(title string) string {
	title = strings.TrimSpace(title)
	if len(title) <= 200 {
		return title
	}
	for len(title) > 197 {
		_, size := utf8.DecodeLastRuneInString(title)
		title = title[:len(title)-size]
	}
	return title + "…"
}
