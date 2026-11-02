package study

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
)

// Collections are organizational records, not independently publishable content.
// Keep the old published wire field as a compatibility projection, but always
// derive it from the current definition and independently public item snapshots.
func catalogSnapshot(tx Tx, collection CatalogCollection) (PublicCatalogCollection, error) {
	return catalogVisible(tx, PublicCatalogCollection{ID: collection.ID, Revision: collection.Revision,
		Title: collection.Draft.Title, Summary: collection.Draft.Summary,
		Chapters: append([]CatalogChapter{}, collection.Draft.Chapters...), UpdatedAt: collection.UpdatedAt})
}
func saveImmediateCatalog(tx Tx, collection *CatalogCollection) error {
	snapshot, err := catalogSnapshot(tx, *collection)
	if err != nil {
		return err
	}
	collection.Published = &snapshot
	if err = recordSave(tx, "catalog_collection", collection.ID, *collection); err != nil {
		return err
	}
	return catalogAppend(tx, *collection, "upsert", collection.UpdatedAt)
}

// Upgrade only already-owned collections. Legacy unowned records stay archived;
// this neither assigns ownership nor publishes any item body. Run at startup so
// existing delta clients receive an upsert for formerly draft/stale collections.
func (s *Service) ReconcileCatalogCollections(ctx context.Context) (changed int, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		records, e := tx.Records("catalog_collection")
		if e != nil {
			return e
		}
		for _, raw := range records {
			owner := recordOwner(raw)
			if owner == "" {
				continue
			}
			user, lookupErr := workspaceUserByID(tx, owner)
			if errors.Is(lookupErr, ErrNotFound) {
				continue
			}
			if lookupErr != nil {
				return lookupErr
			}
			if !user.Enabled {
				continue
			}
			var collection CatalogCollection
			if e = json.Unmarshal(raw, &collection); e != nil {
				return e
			}
			if collection.DeletedAt != nil {
				continue
			}
			base := tx
			if scoped, ok := tx.(*workspaceTx); ok {
				base = scoped.Tx
			}
			e = scopedTransaction(WithWorkspace(ctx, owner), base, func(scoped Tx) error {
				current, e := catalogSnapshot(scoped, collection)
				if e != nil {
					return e
				}
				if collection.Published != nil && reflect.DeepEqual(*collection.Published, current) {
					return nil
				}
				collection.Revision++
				collection.UpdatedAt = s.Now().UTC()
				if e = saveImmediateCatalog(scoped, &collection); e != nil {
					return e
				}
				changed++
				return nil
			})
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		changed = 0
	}
	return
}
