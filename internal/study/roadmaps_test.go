package study

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func roadmapFixture() RoadmapInput {
	root, child := uuid.NewString(), uuid.NewString()
	return RoadmapInput{Title: "学习路线", Summary: "从入门到实践", Nodes: []RoadmapNode{
		{ID: root, Title: "学习路线", ItemIDs: []string{}},
		{ID: child, ParentID: root, Title: "基础知识", Note: "先理解概念", ItemIDs: []string{}},
		{ID: uuid.NewString(), ParentID: child, Title: "实践练习", ItemIDs: []string{}},
	}}
}

func TestRoadmapStoreContract(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			var store Store = NewMemoryStore()
			path := filepath.Join(t.TempDir(), "workspace.json")
			if kind == "file" {
				var err error
				store, err = OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			s := NewService(store)
			item := managedArticle(t, s, "draft", "保留原始资料")
			before, err := s.Changes(ctx, 0, 100, nil)
			if err != nil {
				t.Fatal(err)
			}
			in := roadmapFixture()
			in.Nodes[1].ItemIDs = []string{item.ID}
			created, err := s.SaveLearningRoadmap(ctx, "", in)
			if err != nil || created.Revision != 1 || len(created.Nodes) != 3 || created.ID == "" {
				t.Fatalf("create: %+v %v", created, err)
			}
			// Neither callers nor readers may mutate stored nodes by aliasing.
			in.Nodes[1].Title = "input changed"
			created.Nodes[1].Title = "output changed"
			got, err := s.LearningRoadmap(ctx, created.ID)
			if err != nil || got.Nodes[1].Title != "基础知识" {
				t.Fatalf("read: %+v %v", got, err)
			}
			edit := RoadmapInput{Revision: got.Revision, Title: got.Title, Summary: "修改简介", Nodes: got.Nodes}
			edit.Nodes[1].Collapsed = true
			updated, err := s.SaveLearningRoadmap(ctx, got.ID, edit)
			if err != nil || updated.Revision != 2 || !updated.Nodes[1].Collapsed {
				t.Fatalf("edit: %+v %v", updated, err)
			}
			if _, err = s.SaveLearningRoadmap(ctx, got.ID, edit); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale update: %v", err)
			}
			if err = s.DeleteLearningRoadmap(ctx, got.ID, 1); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale delete: %v", err)
			}
			page, err := s.LearningRoadmaps(ctx, "实践", 12, 0)
			if err != nil || page.Total != 0 {
				t.Fatalf("summary-only search: %+v %v", page, err)
			}
			page, err = s.LearningRoadmaps(ctx, "路线", 12, 0)
			if err != nil || page.Total != 1 || page.Items[0].NodeCount != 3 || page.Items[0].ResourceCount != 1 {
				t.Fatalf("list: %+v %v", page, err)
			}
			if kind == "file" {
				reopened, err := OpenFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				recovered, err := NewService(reopened).LearningRoadmap(ctx, got.ID)
				if err != nil || recovered.Revision != 2 || recovered.Nodes[1].ItemIDs[0] != item.ID || !recovered.Nodes[1].Collapsed {
					t.Fatalf("recovery: %+v %v", recovered, err)
				}
			}
			if err = s.DeleteLearningRoadmap(ctx, got.ID, updated.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err = s.LearningRoadmap(ctx, got.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted visible: %v", err)
			}
			if _, err = s.SaveLearningRoadmap(ctx, got.ID, RoadmapInput{Revision: 3}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("resurrected: %v", err)
			}
			page, err = s.LearningRoadmaps(ctx, "", 12, 0)
			if err != nil || page.Total != 0 || len(page.Items) != 0 {
				t.Fatalf("deleted in list: %+v %v", page, err)
			}
			if _, err = s.Item(ctx, item.ID); err != nil {
				t.Fatalf("map deletion removed resource: %v", err)
			}
			after, err := s.Changes(ctx, 0, 100, nil)
			if err != nil || after.HighWatermark != before.HighWatermark {
				t.Fatalf("map changed public cursor: %+v %v", after, err)
			}
		})
	}
}

func TestRoadmapValidationAndRollback(t *testing.T) {
	s := NewService(NewMemoryStore())
	mutations := map[string]func(*RoadmapInput){
		"no title":   func(v *RoadmapInput) { v.Title = " " },
		"long title": func(v *RoadmapInput) { v.Title = strings.Repeat("学", 201) },
		"no nodes":   func(v *RoadmapInput) { v.Nodes = nil },
		"too many nodes": func(v *RoadmapInput) {
			for len(v.Nodes) <= RoadmapMaxNodes {
				v.Nodes = append(v.Nodes, RoadmapNode{ID: uuid.NewString(), ParentID: v.Nodes[0].ID, Title: "子节点"})
			}
		},
		"duplicate ID":        func(v *RoadmapInput) { v.Nodes[2].ID = v.Nodes[1].ID },
		"invalid ID":          func(v *RoadmapInput) { v.Nodes[1].ID = "not-an-id" },
		"nil ID":              func(v *RoadmapInput) { v.Nodes[1].ID = uuid.Nil.String() },
		"multiple roots":      func(v *RoadmapInput) { v.Nodes[1].ParentID = "" },
		"no root":             func(v *RoadmapInput) { v.Nodes[0].ParentID = v.Nodes[2].ID },
		"self parent":         func(v *RoadmapInput) { v.Nodes[1].ParentID = v.Nodes[1].ID },
		"orphan":              func(v *RoadmapInput) { v.Nodes[1].ParentID = uuid.NewString() },
		"cycle":               func(v *RoadmapInput) { v.Nodes[1].ParentID = v.Nodes[2].ID },
		"root title mismatch": func(v *RoadmapInput) { v.Nodes[0].Title = "不同标题" },
		"empty node title":    func(v *RoadmapInput) { v.Nodes[1].Title = " " },
		"null note":           func(v *RoadmapInput) { v.Nodes[1].Note = "\x00" },
		"large note":          func(v *RoadmapInput) { v.Nodes[1].Note = strings.Repeat("学", 4001) },
		"missing resource":    func(v *RoadmapInput) { v.Nodes[1].ItemIDs = []string{uuid.NewString()} },
		"duplicate resource":  func(v *RoadmapInput) { id := uuid.NewString(); v.Nodes[1].ItemIDs = []string{id, id} },
		"too many resources": func(v *RoadmapInput) {
			for range 21 {
				v.Nodes[1].ItemIDs = append(v.Nodes[1].ItemIDs, uuid.NewString())
			}
		},
		"too deep": func(v *RoadmapInput) {
			for len(v.Nodes) <= RoadmapMaxDepth {
				v.Nodes = append(v.Nodes, RoadmapNode{ID: uuid.NewString(), ParentID: v.Nodes[len(v.Nodes)-1].ID, Title: "深层"})
			}
		},
		"create revision": func(v *RoadmapInput) { v.Revision = 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in := roadmapFixture()
			mutate(&in)
			if _, err := s.SaveLearningRoadmap(t.Context(), "", in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid accepted: %v", err)
			}
		})
	}
	page, err := s.LearningRoadmaps(t.Context(), "", 12, 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("failed writes persisted: %+v %v", page, err)
	}
	in := roadmapFixture()
	in.Title = strings.Repeat("学", 200)
	in.Nodes[0].Title = in.Title
	created, err := s.SaveLearningRoadmap(t.Context(), "", in)
	if err != nil {
		t.Fatalf("Unicode limit: %v", err)
	}
	bad := RoadmapInput{Revision: created.Revision, Title: created.Title, Nodes: created.Nodes}
	bad.Nodes[1].ParentID = uuid.NewString()
	if _, err = s.SaveLearningRoadmap(t.Context(), created.ID, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad edit: %v", err)
	}
	got, err := s.LearningRoadmap(t.Context(), created.ID)
	if err != nil || got.Revision != 1 || got.Nodes[1].ParentID != got.Nodes[0].ID {
		t.Fatalf("rollback: %+v %v", got, err)
	}
	for _, args := range [][2]int{{0, 0}, {51, 0}, {12, -1}, {12, 10001}} {
		if _, err := s.LearningRoadmaps(t.Context(), "", args[0], args[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad page %v: %v", args, err)
		}
	}
}

func TestRoadmapConcurrentRevisionAndDanglingLinks(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := t.Context()
	item := managedArticle(t, s, "draft", "关联资料")
	in := roadmapFixture()
	in.Nodes[1].ItemIDs = []string{item.ID}
	out, err := s.SaveLearningRoadmap(ctx, "", in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SaveLearningRoadmap(ctx, out.ID, RoadmapInput{Revision: 1, Title: out.Title, Summary: "并发编辑", Nodes: out.Nodes})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	saved, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			saved++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if saved != 1 || conflicts != 7 {
		t.Fatalf("concurrent writes: saved=%d conflicts=%d", saved, conflicts)
	}
	// Simulate an independently removed resource; existing links are retained,
	// but a new node cannot acquire that link.
	if err = s.Store.Update(ctx, func(tx Tx) error { now := s.Now(); item.DeletedAt = &now; return tx.SaveItem(item) }); err != nil {
		t.Fatal(err)
	}
	out, err = s.LearningRoadmap(ctx, out.ID)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.SaveLearningRoadmap(ctx, out.ID, RoadmapInput{Revision: out.Revision, Title: out.Title, Nodes: out.Nodes})
	if err != nil || replacement.Nodes[1].ItemIDs[0] != item.ID {
		t.Fatalf("lost old link: %+v %v", replacement, err)
	}
	replacement.Nodes[2].ItemIDs = []string{item.ID}
	if _, err = s.SaveLearningRoadmap(ctx, out.ID, RoadmapInput{Revision: replacement.Revision, Title: replacement.Title, Nodes: replacement.Nodes}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new deleted link accepted: %v", err)
	}
}
