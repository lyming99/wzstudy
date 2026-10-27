package httpapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLearningRoadmapAdminAPI(t *testing.T) {
	_, h, _ := managementApp(t, 1<<20)
	root := uuid.NewString()
	in := study.RoadmapInput{Title: "Go 学习路线", Summary: "循序渐进", Nodes: []study.RoadmapNode{{ID: root, Title: "Go 学习路线"}, {ID: uuid.NewString(), ParentID: root, Title: "基础语法"}}}
	_, reader := managedClient(t, h, "roadmap-reader", []string{"read", "sync", "feedback"})
	var out study.LearningRoadmap
	w := request(t, h, "POST", "/api/v1/admin/roadmaps", adminKey, in)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	data(t, w, &out)
	if out.Revision != 1 || len(out.Nodes) != 2 {
		t.Fatalf("bad roadmap: %+v", out)
	}
	base := "/api/v1/admin/roadmaps/" + out.ID
	for _, key := range []string{"", readKey, reader} {
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/v1/admin/roadmaps", nil}, {"POST", "/api/v1/admin/roadmaps", in},
			{"GET", base, nil}, {"PUT", base, in}, {"POST", base + "/delete", map[string]int64{"revision": out.Revision}},
		} {
			if w := request(t, h, req.method, req.path, key, req.body); w.Code != http.StatusUnauthorized {
				t.Fatalf("non-admin %s %s: %d", req.method, req.path, w.Code)
			}
		}
	}
	if w := request(t, h, "GET", "/api/v1/roadmaps/"+out.ID, reader, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unexpected public route: %d", w.Code)
	}
	var page study.RoadmapPage
	data(t, request(t, h, "GET", "/api/v1/admin/roadmaps?q=Go&limit=12", adminKey, nil), &page)
	if page.Total != 1 || page.Items[0].NodeCount != 2 {
		t.Fatalf("list: %+v", page)
	}
	in.Revision = out.Revision
	in.Nodes[1].Note = "变量、函数与结构体"
	data(t, request(t, h, "PUT", base, adminKey, in), &out)
	if out.Revision != 2 || out.Nodes[1].Note != in.Nodes[1].Note {
		t.Fatalf("edit: %+v", out)
	}
	if w := request(t, h, "PUT", base, adminKey, in); w.Code != http.StatusConflict {
		t.Fatalf("stale update: %d", w.Code)
	}
	if w := request(t, h, "POST", base+"/delete", adminKey, map[string]int64{"revision": 1}); w.Code != http.StatusConflict {
		t.Fatalf("stale delete: %d", w.Code)
	}
	for _, path := range []string{"?limit=0", "?offset=-1", "?limit=bad", "/bad-id"} {
		if w := request(t, h, "GET", "/api/v1/admin/roadmaps"+path, adminKey, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("bad input %s: %d", path, w.Code)
		}
	}
	if w := request(t, h, "POST", "/api/v1/admin/roadmaps", adminKey, map[string]any{"title": "bad", "nodes": []any{}, "unknown": true}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", w.Code)
	}
	data(t, request(t, h, "POST", base+"/delete", adminKey, map[string]int64{"revision": out.Revision}), new(map[string]bool))
	if w := request(t, h, "GET", base, adminKey, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted read: %d", w.Code)
	}
	data(t, request(t, h, "GET", "/api/v1/admin/roadmaps", adminKey, nil), &page)
	if page.Total != 0 {
		t.Fatalf("deleted list: %+v", page)
	}
}
