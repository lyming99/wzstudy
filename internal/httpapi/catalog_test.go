package httpapi

import (
	"net/http"
	"testing"

	"wzstudy/internal/study"
)

func TestCatalogCollectionsPublicationAndIndependentChanges(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	source, sourceKey := create(t, h, false)
	item := ingest(t, h, source.ID, sourceKey, "已发布内容")
	_, reader := managedClient(t, h, "catalog-reader", []string{"read"})
	_, syncer := managedClient(t, h, "catalog-syncer", []string{"sync"})

	body := map[string]any{"revision": 0, "title": "学习合集", "summary": "简介", "chapters": []any{map[string]any{"id": "", "title": "第一章", "item_id": item.ID}}}
	if w := request(t, h, "POST", "/api/v1/workspace/catalog/collections", reader, body); w.Code != http.StatusUnauthorized {
		t.Fatalf("reader wrote catalogue: %d %s", w.Code, w.Body.String())
	}
	var collection study.CatalogCollection
	data(t, request(t, h, "POST", "/api/v1/workspace/catalog/collections", workspaceKey, body), &collection)
	if collection.Revision != 1 || len(collection.Draft.Chapters) != 1 || collection.Draft.Chapters[0].ID == "" {
		t.Fatalf("invalid draft: %+v", collection)
	}
	if w := request(t, h, "GET", "/api/v1/catalog/collections/"+collection.ID, reader, nil); w.Code != http.StatusNotFound {
		t.Fatalf("draft became public: %d %s", w.Code, w.Body.String())
	}
	before, err := service.Changes(t.Context(), 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	data(t, request(t, h, "POST", "/api/v1/workspace/catalog/collections/"+collection.ID+"/publish", workspaceKey, map[string]int64{"revision": collection.Revision}), &collection)
	if collection.Published == nil || collection.Revision != 2 {
		t.Fatalf("not published: %+v", collection)
	}
	var published study.PublicCatalogCollection
	data(t, request(t, h, "GET", "/api/v1/catalog/collections/"+collection.ID, reader, nil), &published)
	if published.Title != "学习合集" || len(published.Chapters) != 1 || published.Chapters[0].ItemID != item.ID {
		t.Fatalf("wrong public snapshot: %+v", published)
	}
	if w := request(t, h, "GET", "/api/v1/catalog/collections/changes?after=0", reader, nil); w.Code != http.StatusForbidden {
		t.Fatalf("read-only identity accessed changes: %d %s", w.Code, w.Body.String())
	}
	var changes study.CatalogChangePage
	data(t, request(t, h, "GET", "/api/v1/catalog/collections/changes?after=0", syncer, nil), &changes)
	if len(changes.Changes) != 1 || changes.Changes[0].Operation != "upsert" || changes.NextCursor != 1 {
		t.Fatalf("wrong catalog changes: %+v", changes)
	}
	after, err := service.Changes(t.Context(), 0, 100, nil)
	if err != nil || before.HighWatermark != after.HighWatermark {
		t.Fatalf("catalog changed legacy content cursor: %+v %+v %v", before, after, err)
	}

	// Editing a draft never changes the published snapshot until republished.
	edit := map[string]any{"revision": collection.Revision, "title": "尚未发布的新标题", "summary": "", "chapters": collection.Draft.Chapters}
	data(t, request(t, h, "PUT", "/api/v1/workspace/catalog/collections/"+collection.ID, workspaceKey, edit), &collection)
	data(t, request(t, h, "GET", "/api/v1/catalog/collections/"+collection.ID, reader, nil), &published)
	if published.Title != "学习合集" {
		t.Fatalf("unpublished draft leaked: %+v", published)
	}
	if w := request(t, h, "POST", "/api/v1/workspace/catalog/collections/"+collection.ID+"/publish", workspaceKey, map[string]int64{"revision": collection.Revision - 1}); w.Code != http.StatusConflict {
		t.Fatalf("stale revision accepted: %d %s", w.Code, w.Body.String())
	}

	// Withdrawing an item invalidates its chapter in the catalogue and emits a
	// separate collection update; old snapshots must not disclose its chapter.
	var unpublished study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items/"+item.ID+"/unpublish", workspaceKey, map[string]int64{"revision": item.Revision}), &unpublished)
	data(t, request(t, h, "GET", "/api/v1/catalog/collections/"+collection.ID, reader, nil), &published)
	if len(published.Chapters) != 0 {
		t.Fatalf("withdrawn item leaked: %+v", published)
	}
	data(t, request(t, h, "GET", "/api/v1/catalog/collections/changes?after=0", syncer, nil), &changes)
	if len(changes.Changes) != 2 || changes.Changes[0].Operation != "delete" || changes.Changes[1].Collection == nil || len(changes.Changes[1].Collection.Chapters) != 0 {
		t.Fatalf("stale catalogue snapshots were not invalidated: %+v", changes)
	}
	data(t, request(t, h, "GET", "/api/v1/workspace/catalog/collections/"+collection.ID, workspaceKey, nil), &collection)
	if w := request(t, h, "POST", "/api/v1/workspace/catalog/collections/"+collection.ID+"/publish", workspaceKey, map[string]int64{"revision": collection.Revision}); w.Code != http.StatusBadRequest {
		t.Fatalf("unpublished chapter was publishable: %d %s", w.Code, w.Body.String())
	}
	data(t, request(t, h, "POST", "/api/v1/workspace/catalog/collections/"+collection.ID+"/delete", workspaceKey, map[string]int64{"revision": collection.Revision}), new(map[string]bool))
	if w := request(t, h, "GET", "/api/v1/catalog/collections/"+collection.ID, reader, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted catalog visible: %d %s", w.Code, w.Body.String())
	}
	if _, err := service.Item(t.Context(), item.ID); err != nil {
		t.Fatalf("deleting collection deleted item: %v", err)
	}
}
