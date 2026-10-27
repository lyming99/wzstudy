package study

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Catalog collections are administrator-managed published content, not the
// user's personal collections. Legacy "collection" records are left untouched.
type CatalogChapter struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	ItemID string `json:"item_id"`
}

type CatalogDefinition struct {
	Title    string           `json:"title"`
	Summary  string           `json:"summary"`
	Chapters []CatalogChapter `json:"chapters"`
}

type PublicCatalogCollection struct {
	ID        string           `json:"id"`
	Revision  int64            `json:"revision"`
	Title     string           `json:"title"`
	Summary   string           `json:"summary"`
	Chapters  []CatalogChapter `json:"chapters"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type CatalogCollection struct {
	ID        string                   `json:"id"`
	Revision  int64                    `json:"revision"`
	Draft     CatalogDefinition        `json:"draft"`
	Published *PublicCatalogCollection `json:"published,omitempty"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
	DeletedAt *time.Time               `json:"deleted_at,omitempty"`
}

type CatalogInput struct {
	Revision int64            `json:"revision"`
	Title    string           `json:"title"`
	Summary  string           `json:"summary"`
	Chapters []CatalogChapter `json:"chapters"`
}

type CatalogChange struct {
	Cursor     int64                    `json:"cursor"`
	Operation  string                   `json:"operation"`
	ID         string                   `json:"id"`
	Revision   int64                    `json:"revision"`
	Collection *PublicCatalogCollection `json:"collection,omitempty"`
	CreatedAt  time.Time                `json:"created_at"`
}

type CatalogChangePage struct {
	Changes       []CatalogChange `json:"changes"`
	NextCursor    int64           `json:"next_cursor"`
	HighWatermark int64           `json:"high_watermark"`
	HasMore       bool            `json:"has_more"`
}

type catalogClock struct {
	Cursor int64 `json:"cursor"`
}

type CatalogAdminPage struct {
	Items  []CatalogCollection `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type CatalogPublicPage struct {
	Items  []PublicCatalogCollection `json:"items"`
	Total  int                       `json:"total"`
	Limit  int                       `json:"limit"`
	Offset int                       `json:"offset"`
}

func validCatalogID(value string) (string, bool) {
	id, err := uuid.Parse(value)
	return id.String(), err == nil && id != uuid.Nil
}

func normalizeCatalog(tx Tx, in CatalogInput) (CatalogDefinition, error) {
	in.Title, in.Summary = strings.TrimSpace(in.Title), strings.TrimSpace(in.Summary)
	if in.Title == "" || !validText(in.Title, 500) || !validText(in.Summary, 8000) || len(in.Chapters) > 100 {
		return CatalogDefinition{}, ErrInvalid
	}
	out := CatalogDefinition{Title: in.Title, Summary: in.Summary, Chapters: make([]CatalogChapter, 0, len(in.Chapters))}
	ids, items := map[string]bool{}, map[string]bool{}
	for _, chapter := range in.Chapters {
		chapter.Title = strings.TrimSpace(chapter.Title)
		if chapter.Title == "" || !validText(chapter.Title, 200) {
			return CatalogDefinition{}, ErrInvalid
		}
		if chapter.ID == "" {
			chapter.ID = uuid.NewString()
		}
		var ok bool
		chapter.ID, ok = validCatalogID(chapter.ID)
		if !ok || ids[chapter.ID] {
			return CatalogDefinition{}, ErrInvalid
		}
		chapter.ItemID, ok = validCatalogID(chapter.ItemID)
		if !ok || items[chapter.ItemID] {
			return CatalogDefinition{}, ErrInvalid
		}
		item, err := tx.Item(chapter.ItemID)
		if errors.Is(err, ErrNotFound) || (err == nil && item.DeletedAt != nil) {
			return CatalogDefinition{}, ErrInvalid
		}
		if err != nil {
			return CatalogDefinition{}, err
		}
		ids[chapter.ID], items[chapter.ItemID] = true, true
		out.Chapters = append(out.Chapters, chapter)
	}
	return out, nil
}

func catalogCollection(tx Tx, id string) (CatalogCollection, error) {
	id, ok := validCatalogID(id)
	if !ok {
		return CatalogCollection{}, ErrInvalid
	}
	v, err := recordAs[CatalogCollection](tx, "catalog_collection", id)
	if err != nil {
		return v, err
	}
	if v.DeletedAt != nil {
		return CatalogCollection{}, ErrNotFound
	}
	return v, nil
}

func (s *Service) SaveCatalogCollection(ctx context.Context, id string, in CatalogInput) (out CatalogCollection, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		now := s.Now().UTC()
		if id == "" {
			if in.Revision != 0 {
				return ErrInvalid
			}
			out = CatalogCollection{ID: uuid.NewString(), CreatedAt: now}
		} else {
			var e error
			out, e = catalogCollection(tx, id)
			if e != nil {
				return e
			}
			if out.Revision != in.Revision {
				return ErrConflict
			}
		}
		definition, e := normalizeCatalog(tx, in)
		if e != nil {
			return e
		}
		out.Draft, out.UpdatedAt = definition, now
		out.Revision++
		return recordSave(tx, "catalog_collection", out.ID, out)
	})
	if err != nil {
		out = CatalogCollection{}
	}
	return
}

func catalogAppend(tx Tx, collection CatalogCollection, operation string, now time.Time) error {
	clock, err := recordAs[catalogClock](tx, "catalog_clock", "global")
	if errors.Is(err, ErrNotFound) {
		clock = catalogClock{}
	} else if err != nil {
		return err
	}
	clock.Cursor++
	change := CatalogChange{Cursor: clock.Cursor, Operation: operation, ID: collection.ID, Revision: collection.Revision, CreatedAt: now}
	if operation == "upsert" {
		change.Collection = collection.Published
	}
	if err = recordSave(tx, "catalog_change", fmt.Sprintf("%020d", clock.Cursor), change); err != nil {
		return err
	}
	return recordSave(tx, "catalog_clock", "global", clock)
}

// A published item's visibility changes independently of its collection. Emit
// a new collection snapshot in the same transaction so cached catalogues can
// remove or restore the chapter without changing the legacy content cursor.
func refreshCatalogReferences(tx Tx, itemID string, now time.Time) error {
	collections, err := recordsAs[CatalogCollection](tx, "catalog_collection")
	if err != nil {
		return err
	}
	for _, collection := range collections {
		if collection.DeletedAt != nil || collection.Published == nil {
			continue
		}
		found := false
		for _, chapter := range collection.Published.Chapters {
			if chapter.ItemID == itemID {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		collection.Revision++
		collection.UpdatedAt = now
		collection.Published.Revision = collection.Revision
		collection.Published.UpdatedAt = now
		if err = recordSave(tx, "catalog_collection", collection.ID, collection); err != nil {
			return err
		}
		if err = catalogAppend(tx, collection, "upsert", now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) PublishCatalogCollection(ctx context.Context, id string, revision int64, publish bool) (out CatalogCollection, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		var e error
		out, e = catalogCollection(tx, id)
		if e != nil {
			return e
		}
		if out.Revision != revision {
			return ErrConflict
		}
		if publish {
			if len(out.Draft.Chapters) == 0 {
				return ErrInvalid
			}
			for _, chapter := range out.Draft.Chapters {
				item, itemErr := tx.Item(chapter.ItemID)
				if itemErr != nil {
					if errors.Is(itemErr, ErrNotFound) {
						return ErrInvalid
					}
					return itemErr
				}
				if item.DeletedAt != nil || item.Published == nil {
					return ErrInvalid
				}
			}
		}
		if !publish && out.Published == nil {
			return nil
		}
		now := s.Now().UTC()
		out.Revision++
		out.UpdatedAt = now
		operation := "delete"
		if publish {
			operation = "upsert"
			out.Published = &PublicCatalogCollection{ID: out.ID, Revision: out.Revision, Title: out.Draft.Title, Summary: out.Draft.Summary, Chapters: append([]CatalogChapter(nil), out.Draft.Chapters...), UpdatedAt: now}
		} else {
			out.Published = nil
		}
		if e = recordSave(tx, "catalog_collection", out.ID, out); e != nil {
			return e
		}
		return catalogAppend(tx, out, operation, now)
	})
	if err != nil {
		out = CatalogCollection{}
	}
	return
}

func (s *Service) DeleteCatalogCollection(ctx context.Context, id string, revision int64) error {
	return s.Store.Update(ctx, func(tx Tx) error {
		out, err := catalogCollection(tx, id)
		if err != nil {
			return err
		}
		if out.Revision != revision {
			return ErrConflict
		}
		now := s.Now().UTC()
		wasPublished := out.Published != nil
		out.Revision++
		out.UpdatedAt, out.DeletedAt, out.Published = now, &now, nil
		if err = recordSave(tx, "catalog_collection", out.ID, out); err != nil {
			return err
		}
		if wasPublished {
			return catalogAppend(tx, out, "delete", now)
		}
		return nil
	})
}

func (s *Service) AdminCatalogCollection(ctx context.Context, id string) (out CatalogCollection, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		out, err = catalogCollection(tx, id)
		return err
	})
	return
}

func catalogVisible(tx Tx, source PublicCatalogCollection) (PublicCatalogCollection, error) {
	out := source
	out.Chapters = make([]CatalogChapter, 0, len(source.Chapters))
	for _, chapter := range source.Chapters {
		item, err := tx.Item(chapter.ItemID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return out, err
		}
		if item.DeletedAt == nil && item.Published != nil {
			out.Chapters = append(out.Chapters, chapter)
		}
	}
	return out, nil
}

func validCatalogPage(limit, offset int) bool {
	return limit >= 1 && limit <= 50 && offset >= 0 && offset <= 10000
}

func (s *Service) AdminCatalogCollections(ctx context.Context, limit, offset int) (out CatalogAdminPage, err error) {
	if !validCatalogPage(limit, offset) {
		return out, ErrInvalid
	}
	out = CatalogAdminPage{Items: []CatalogCollection{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[CatalogCollection](tx, "catalog_collection")
		if e != nil {
			return e
		}
		filtered := make([]CatalogCollection, 0, len(all))
		for _, item := range all {
			if item.DeletedAt == nil {
				filtered = append(filtered, item)
			}
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

func (s *Service) PublicCatalogCollection(ctx context.Context, id string) (out PublicCatalogCollection, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		v, e := catalogCollection(tx, id)
		if e != nil {
			return e
		}
		if v.Published == nil {
			return ErrNotFound
		}
		out, e = catalogVisible(tx, *v.Published)
		return e
	})
	return
}

func (s *Service) PublicCatalogCollections(ctx context.Context, limit, offset int) (out CatalogPublicPage, err error) {
	if !validCatalogPage(limit, offset) {
		return out, ErrInvalid
	}
	out = CatalogPublicPage{Items: []PublicCatalogCollection{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[CatalogCollection](tx, "catalog_collection")
		if e != nil {
			return e
		}
		filtered := []PublicCatalogCollection{}
		for _, v := range all {
			if v.DeletedAt == nil && v.Published != nil {
				summary := *v.Published
				summary.Chapters = nil // List pages are bounded; details are fetched by ID.
				filtered = append(filtered, summary)
			}
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

func (s *Service) CatalogChanges(ctx context.Context, after int64, limit int) (out CatalogChangePage, err error) {
	if after < 0 || limit < 1 || limit > 20 {
		return out, ErrInvalid
	}
	out = CatalogChangePage{Changes: []CatalogChange{}, NextCursor: after}
	err = s.Store.View(ctx, func(tx Tx) error {
		clock, e := recordAs[catalogClock](tx, "catalog_clock", "global")
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		out.HighWatermark = clock.Cursor
		if after > clock.Cursor {
			return ErrInvalid
		}
		all, e := recordsAs[CatalogChange](tx, "catalog_change")
		if e != nil {
			return e
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Cursor < all[j].Cursor })
		for _, change := range all {
			if change.Cursor <= after {
				continue
			}
			if change.Collection != nil {
				current, currentErr := catalogCollection(tx, change.ID)
				if currentErr != nil && !errors.Is(currentErr, ErrNotFound) {
					return currentErr
				}
				// Do not expose superseded or unpublished snapshots to new readers.
				if errors.Is(currentErr, ErrNotFound) || current.Published == nil || current.Published.Revision != change.Collection.Revision {
					change.Operation, change.Collection = "delete", nil
				} else {
					visible, visibleErr := catalogVisible(tx, *change.Collection)
					if visibleErr != nil {
						return visibleErr
					}
					change.Collection = &visible
				}
			}
			out.Changes = append(out.Changes, change)
			out.NextCursor = change.Cursor
			if len(out.Changes) == limit {
				break
			}
		}
		out.HasMore = out.NextCursor < out.HighWatermark
		return nil
	})
	return
}
