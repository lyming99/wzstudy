package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestPersonalCollectionIsolationIdempotencyAndWithdrawal(t *testing.T) {
	service, h, _ := managementApp(t, 1<<20)
	item, err := service.SaveManaged(testWorkspaceContext(), "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "公开内容", Kind: "article", Content: "私密草稿不可泄漏"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Publication(testWorkspaceContext(), item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	client, key := workspaceLearner(t, h, "personal-owner")
	_, other := managedLearner(t, h, "personal-other")
	_, reader := managedClient(t, h, "personal-read", []string{"read"})
	_, source := create(t, h, false)
	path := "/api/v1/client/learning/collections"
	commands := path + "/commands"
	collectionID := uuid.NewString()
	createID := uuid.NewString()
	createBody := map[string]any{"event_id": createID, "collection_id": collectionID, "action": "create", "expected_revision": 0, "title": "我的合集"}
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {adminKey, 401}, {source, 401}, {reader, 401}} {
		if w := request(t, h, "POST", commands, tc.key, createBody); w.Code != tc.status {
			t.Fatalf("write authority: %d %s", w.Code, w.Body.String())
		}
	}
	var created study.PersonalCollectionAck
	data(t, request(t, h, "POST", commands, key, createBody), &created)
	if created.Duplicate || created.Collection.ClientID != client.ID || created.Collection.Revision != 1 || len(created.Collection.Items) != 0 {
		t.Fatalf("create: %+v", created)
	}
	var repeated study.PersonalCollectionAck
	data(t, request(t, h, "POST", commands, key, createBody), &repeated)
	if !repeated.Duplicate || repeated.Collection.Revision != 1 {
		t.Fatalf("duplicate: %+v", repeated)
	}
	changedBody := map[string]any{"event_id": createID, "collection_id": collectionID, "action": "create", "expected_revision": 0, "title": "篡改合集"}
	if w := request(t, h, "POST", commands, key, changedBody); w.Code != http.StatusConflict {
		t.Fatalf("event collision: %d %s", w.Code, w.Body.String())
	}
	spoof := map[string]any{"event_id": uuid.NewString(), "collection_id": collectionID, "action": "create", "expected_revision": 0, "title": "伪造", "client_id": client.ID}
	if w := request(t, h, "POST", commands, other, spoof); w.Code != http.StatusBadRequest {
		t.Fatalf("body spoof: %d", w.Code)
	}
	if w := request(t, h, "GET", path+"?client_id="+client.ID, other, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("query spoof: %d", w.Code)
	}
	if w := request(t, h, "GET", path+"/"+collectionID, other, nil); w.Code != http.StatusNotFound {
		t.Fatalf("cross-client: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, h, "GET", path, reader, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("private read: %d", w.Code)
	}
	addID := uuid.NewString()
	add := map[string]any{"event_id": addID, "collection_id": collectionID, "action": "add", "expected_revision": 1, "item_id": item.ID}
	var added study.PersonalCollectionAck
	data(t, request(t, h, "POST", commands, key, add), &added)
	if added.Collection.Revision != 2 || len(added.Collection.Items) != 1 {
		t.Fatalf("add: %+v", added)
	}
	if w := request(t, h, "POST", commands, key, map[string]any{"event_id": uuid.NewString(), "collection_id": collectionID, "action": "rename", "expected_revision": 1, "title": "冲突"}); w.Code != 409 {
		t.Fatalf("revision: %d", w.Code)
	}
	var page study.PersonalCollectionPage
	data(t, request(t, h, "GET", path, key, nil), &page)
	if page.ClientID != client.ID || page.Total != 1 || len(page.Items[0].Items) != 1 || !page.Items[0].Items[0].Available {
		t.Fatalf("list: %+v", page)
	}
	item, err = service.Publication(testWorkspaceContext(), item.ID, item.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", path+"/"+collectionID, key, nil)
	if strings.Contains(w.Body.String(), "私密草稿不可泄漏") {
		t.Fatal("unpublished content leaked")
	}
	var view study.PersonalCollectionView
	data(t, w, &view)
	if len(view.Items) != 1 || view.Items[0].Available {
		t.Fatalf("unavailable placeholder: %+v", view)
	}
	data(t, request(t, h, "POST", commands, key, add), &repeated)
	if !repeated.Duplicate || repeated.Collection.Revision != 2 {
		t.Fatalf("retry after unpublish: %+v", repeated)
	}
	remove := map[string]any{"event_id": uuid.NewString(), "collection_id": collectionID, "action": "remove", "expected_revision": 2, "item_id": item.ID}
	var removed study.PersonalCollectionAck
	data(t, request(t, h, "POST", commands, key, remove), &removed)
	if removed.Collection.Revision != 3 || len(removed.Collection.Items) != 0 {
		t.Fatalf("remove unavailable: %+v", removed)
	}
	deleteBody := map[string]any{"event_id": uuid.NewString(), "collection_id": collectionID, "action": "delete", "expected_revision": 3}
	var deleted study.PersonalCollectionAck
	data(t, request(t, h, "POST", commands, key, deleteBody), &deleted)
	if deleted.Collection.DeletedAt == nil || deleted.Collection.Revision != 4 {
		t.Fatalf("delete: %+v", deleted)
	}
	if w := request(t, h, "GET", path+"/"+collectionID, key, nil); w.Code != 404 {
		t.Fatalf("deleted visible: %d", w.Code)
	}
	data(t, request(t, h, "POST", commands, key, createBody), &repeated)
	if !repeated.Duplicate || repeated.Collection.Revision != 1 {
		t.Fatalf("retry after delete: %+v", repeated)
	}
	if w := request(t, h, "POST", commands, key, map[string]any{"event_id": uuid.NewString(), "collection_id": collectionID, "action": "create", "expected_revision": 0, "title": "重建"}); w.Code != 409 {
		t.Fatalf("tombstone overwritten: %d", w.Code)
	}
}
