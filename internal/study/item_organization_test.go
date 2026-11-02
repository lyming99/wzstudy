package study

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

func TestImmediateCollectionsAndAtomicItemOrganization(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var store Store = NewMemoryStore()
			if kind == "file" {
				var e error
				store, e = OpenFileStore(filepath.Join(t.TempDir(), "workspace.json"))
				if e != nil {
					t.Fatal(e)
				}
			}
			defer store.Close()
			s := NewService(store)
			first := approveManaged(t, s, managedArticle(t, s, "pending", "first"))
			second := approveManaged(t, s, managedArticle(t, s, "pending", "second"))
			private := managedArticle(t, s, "draft", "private")
			_, key := feedbackClient(t, s, []string{"read", "feedback"})
			if _, e := s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: first.ID, Action: "view"}}); e != nil {
				t.Fatal(e)
			}
			a, e := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "原合集", Chapters: []CatalogChapter{{Title: "first", ItemID: first.ID}, {Title: "未公开资料", ItemID: private.ID}}})
			if e != nil || a.Published == nil {
				t.Fatal(a, e)
			}
			visible, e := s.PublicCatalogCollection(ctx, a.ID)
			if e != nil || len(visible.Chapters) != 1 {
				t.Fatal("collection was not immediately visible or leaked private item", visible, e)
			}
			a, e = s.SaveCatalogCollection(ctx, a.ID, CatalogInput{Revision: a.Revision, Title: "更新后合集", Chapters: a.Draft.Chapters})
			visible, e = s.PublicCatalogCollection(ctx, a.ID)
			if e != nil || visible.Title != "更新后合集" {
				t.Fatal(visible, e)
			}
			b, e := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "空目标合集"})
			if e != nil {
				t.Fatal(e)
			}
			if v, e := s.PublicCatalogCollection(ctx, b.ID); e != nil || len(v.Chapters) != 0 {
				t.Fatal(v, e)
			}
			target := b.ID
			result, e := s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{first.ID, first.Revision}}, CollectionID: &target, CollectionRevision: b.Revision})
			if e != nil || result.Moved != 1 || result.CollectionsUpdated != 2 || result.Items[0].Revision != first.Revision {
				t.Fatal(result, e)
			}
			left, _ := s.AdminCatalogCollection(ctx, a.ID)
			right, _ := s.PublicCatalogCollection(ctx, b.ID)
			if len(left.Draft.Chapters) != 1 || left.Draft.Chapters[0].ItemID != private.ID || len(right.Chapters) != 1 || right.Chapters[0].ItemID != first.ID {
				t.Fatal(left, right)
			}
			learning, e := s.ClientLearning(ctx, key, "", []string{first.ID}, 10, 0)
			if e != nil || learning.Items[0].ViewCount != 1 {
				t.Fatal("move changed learning state", learning, e)
			}
			// Classification is immediately queryable but must never leak pending edits.
			draft := first.Draft
			draft.Title = "unreviewed private title"
			draft.Content = "unreviewed private body"
			pending, e := s.SaveManaged(ctx, first.ID, ManagedInput{Revision: first.Revision, SourceID: first.SourceID, Status: "pending", Content: draft}, nil)
			if e != nil {
				t.Fatal(e)
			}
			category := "人工智能"
			result, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{pending.ID, pending.Revision}}, Category: &category})
			if e != nil || result.CategoryUpdated != 1 {
				t.Fatal(result, e)
			}
			updated, e := s.Item(ctx, first.ID)
			if e != nil || updated.Draft.Title != draft.Title || updated.Published.Title != first.Published.Title || updated.Published.Content.Content != first.Published.Content.Content || updated.Draft.Category != category || updated.Published.Category != category || updated.ID != first.ID || updated.ExternalID != first.ExternalID {
				t.Fatal(updated, e)
			}
			news, e := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "news", Limit: 100})
			if e != nil {
				t.Fatal(e)
			}
			inNews := false
			for _, item := range news.Items {
				if item.ID == first.ID {
					inNews = true
				}
			}
			if !inNews {
				t.Fatal("topic category incorrectly removed default-mode content from news")
			}

			// An atomic batch rejects a stale member before changing the first member.
			empty := ""
			target = a.ID
			_, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{updated.ID, updated.Revision}, {second.ID, second.Revision + 1}}, Category: &empty, CollectionID: &target, CollectionRevision: left.Revision})
			if !errors.Is(e, ErrConflict) {
				t.Fatal("stale batch accepted", e)
			}
			unchanged, _ := s.Item(ctx, first.ID)
			right, _ = s.PublicCatalogCollection(ctx, b.ID)
			if unchanged.Revision != updated.Revision || unchanged.Published.Category != category || len(right.Chapters) != 1 {
				t.Fatal("partial batch mutation", unchanged, right)
			}
			result, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{updated.ID, updated.Revision}}, Category: &empty})
			if e != nil || result.CategoryUpdated != 1 {
				t.Fatal(result, e)
			}
			news, e = s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "news", Category: "stale-category-picker", Limit: 100})
			found := false
			for _, item := range news.Items {
				if item.ID == first.ID {
					found = true
				}
			}
			if e != nil || !found {
				t.Fatal("empty category not queryable by news", news, e)
			}
			remove := ""
			after, _ := s.Item(ctx, first.ID)
			result, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{after.ID, after.Revision}}, CollectionID: &remove})
			if e != nil || result.Moved != 1 {
				t.Fatal(result, e)
			}
			right, _ = s.PublicCatalogCollection(ctx, b.ID)
			if len(right.Chapters) != 0 {
				t.Fatal(right)
			}
			// Source category defaults cannot prevent an explicit management clear.
			clearDraft := after.Draft
			clearDraft.Category = ""
			managed, e := s.SaveManaged(ctx, after.ID, ManagedInput{Revision: after.Revision, SourceID: after.SourceID, Status: "pending", Content: clearDraft}, nil)
			if e != nil || managed.Draft.Category != "" {
				t.Fatal(managed, e)
			}
		})
	}
}

func TestImmediateCollectionReconciliationPreservesWorkspaceAndUnownedArchive(t *testing.T) {
	s := NewService(NewMemoryStore())
	base := context.Background()
	admin, _, e := s.EnsureAdministrator(base, AdministratorSettings{Email: "organization-admin@example.test", Password: "organization-administrator-password", DisplayName: "管理员"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := WithWorkspace(base, admin.ID)
	owned, e := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "legacy draft"})
	if e != nil {
		t.Fatal(e)
	}
	owned.Published = nil
	if e = s.Store.Update(ctx, func(tx Tx) error { return recordSave(tx, "catalog_collection", owned.ID, owned) }); e != nil {
		t.Fatal(e)
	}
	unowned, e := s.SaveCatalogCollection(base, "", CatalogInput{Title: "archived unowned"})
	if e != nil {
		t.Fatal(e)
	}
	unowned.Published = nil
	if e = s.Store.Update(base, func(tx Tx) error { return recordSave(tx, "catalog_collection", unowned.ID, unowned) }); e != nil {
		t.Fatal(e)
	}
	count, e := s.ReconcileCatalogCollections(base)
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
	visible, e := s.PublicCatalogCollection(ctx, owned.ID)
	if e != nil || visible.Title != "legacy draft" || visible.Revision != owned.Revision+1 {
		t.Fatal(visible, e)
	}
	archived, e := s.AdminCatalogCollection(base, unowned.ID)
	if e != nil || archived.Published != nil || archived.Revision != unowned.Revision {
		t.Fatal("unowned data assigned or exposed", archived, e)
	}
	if e = s.Store.View(ctx, func(tx Tx) error {
		raw, e := tx.Record("catalog_collection", owned.ID)
		if e == nil && recordOwner(raw) != admin.ID {
			t.Fatal("workspace owner marker changed")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if count, e = s.ReconcileCatalogCollections(base); e != nil || count != 0 {
		t.Fatal("upgrade is not idempotent", count, e)
	}
	changes, e := s.CatalogChanges(ctx, 0, 20)
	if e != nil || len(changes.Changes) == 0 || changes.Changes[len(changes.Changes)-1].Operation != "upsert" {
		t.Fatal(changes, e)
	}
}

func TestOrganizationValidationAndTargetCapacity(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	first := approveManaged(t, s, managedArticle(t, s, "pending", "first"))
	for _, in := range []ItemOrganizationInput{
		{Items: []OrganizationItem{{first.ID, first.Revision}}},
		{Items: []OrganizationItem{{first.ID, first.Revision}, {first.ID, first.Revision}}, Category: ptrString("分类")},
		{Items: []OrganizationItem{{first.ID, first.Revision}}, CollectionID: ptrString("invalid"), CollectionRevision: 1},
	} {
		if _, e := s.OrganizeItems(ctx, in); !errors.Is(e, ErrInvalid) {
			t.Fatal(in, e)
		}
	}
	chapters := []CatalogChapter{}
	if e := s.Store.Update(ctx, func(tx Tx) error {
		for i := 0; i < 100; i++ {
			id := uuid.NewString()
			item := first
			item.ID = id
			item.ExternalID = id
			item.Draft.ExternalID = id
			item.Published = nil
			if e := tx.SaveItem(item); e != nil {
				return e
			}
			chapters = append(chapters, CatalogChapter{ID: uuid.NewString(), Title: id, ItemID: id})
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	full, e := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "满合集", Chapters: chapters})
	if e != nil {
		t.Fatal(e)
	}
	target := full.ID
	if _, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{first.ID, first.Revision}}, CollectionID: &target, CollectionRevision: full.Revision}); !errors.Is(e, ErrInvalid) {
		t.Fatal("full target accepted", e)
	}
	after, _ := s.AdminCatalogCollection(ctx, full.ID)
	if len(after.Draft.Chapters) != 100 || after.Revision != full.Revision {
		t.Fatal("full target partially modified", after)
	}
}

func ptrString(value string) *string { return &value }

func TestLegacyClassificationPreservesPendingReviewState(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	item := approveManaged(t, s, managedArticle(t, s, "pending", "public title"))
	content := item.Draft
	content.Title = "private pending title"
	pending, e := s.SaveManaged(ctx, item.ID, ManagedInput{Revision: item.Revision, SourceID: item.SourceID, Status: "pending", Content: content}, nil)
	if e != nil {
		t.Fatal(e)
	}
	pending.Status = ""
	if e = s.Store.Update(ctx, func(tx Tx) error { return tx.SaveItem(pending) }); e != nil {
		t.Fatal(e)
	}
	category := "学习分类"
	if _, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{pending.ID, pending.Revision}}, Category: &category}); e != nil {
		t.Fatal(e)
	}
	after, e := s.Item(ctx, pending.ID)
	if e != nil || ReviewState(after) != "pending" || after.Published.Title != item.Published.Title || after.Draft.Title != content.Title {
		t.Fatal(after, e)
	}
}

func TestMoveLongItemTitleCreatesEditableCollection(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	title := strings.Repeat("课程标题", 30)
	item := approveManaged(t, s, managedArticle(t, s, "pending", title))
	collection, e := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "合集"})
	if e != nil {
		t.Fatal(e)
	}
	id := collection.ID
	if _, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{item.ID, item.Revision}}, CollectionID: &id, CollectionRevision: collection.Revision}); e != nil {
		t.Fatal(e)
	}
	after, e := s.AdminCatalogCollection(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	label := after.Draft.Chapters[0].Title
	if len(label) > 200 || !utf8.ValidString(label) || !strings.HasSuffix(label, "…") {
		t.Fatal(label)
	}
	if _, e = s.SaveCatalogCollection(ctx, id, CatalogInput{Revision: after.Revision, Title: after.Draft.Title, Chapters: after.Draft.Chapters}); e != nil {
		t.Fatal("moved collection cannot be edited", e)
	}
	original, _ := s.Item(ctx, item.ID)
	if original.Draft.Title != title {
		t.Fatal("move truncated the original item title")
	}
}

func TestPendingMovedChapterAppearsOnlyAfterMaterialPublication(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	item := managedArticle(t, s, "pending", "待公开资料")
	collection, e := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "合集"})
	if e != nil {
		t.Fatal(e)
	}
	target := collection.ID
	if _, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{item.ID, item.Revision}}, CollectionID: &target, CollectionRevision: collection.Revision}); e != nil {
		t.Fatal(e)
	}
	before, e := s.PublicCatalogCollection(ctx, target)
	if e != nil || len(before.Chapters) != 0 {
		t.Fatal("private chapter exposed", before, e)
	}
	item = approveManaged(t, s, item)
	after, e := s.PublicCatalogCollection(ctx, target)
	if e != nil || len(after.Chapters) != 1 || after.Chapters[0].ItemID != item.ID {
		t.Fatal("newly public chapter missing", after, e)
	}
	if _, e = s.Publication(ctx, item.ID, item.Revision, false); e != nil {
		t.Fatal(e)
	}
	after, e = s.PublicCatalogCollection(ctx, target)
	if e != nil || len(after.Chapters) != 0 {
		t.Fatal("withdrawn chapter exposed", after, e)
	}
}

type organizationFaultStore struct {
	Store
	failCatalogChange bool
}
type organizationFaultTx struct {
	Tx
	store *organizationFaultStore
}

func (s *organizationFaultStore) Update(ctx context.Context, fn func(Tx) error) error {
	return s.Store.Update(ctx, func(tx Tx) error { return fn(&organizationFaultTx{Tx: tx, store: s}) })
}
func (tx *organizationFaultTx) SaveRecord(kind, id string, raw json.RawMessage) error {
	if tx.store.failCatalogChange && kind == "catalog_change" {
		return ErrUnauthorized
	}
	return tx.Tx.SaveRecord(kind, id, raw)
}
func TestReconcileRollsBackWhenEventStorageFailsAfterSavingProjection(t *testing.T) {
	store := &organizationFaultStore{Store: NewMemoryStore()}
	s := NewService(store)
	ctx := context.Background()
	admin, _, e := s.EnsureAdministrator(ctx, AdministratorSettings{Email: "fault-admin@example.test", Password: "fault-test-administrator-password", DisplayName: "管理员"})
	if e != nil {
		t.Fatal(e)
	}
	owned := WithWorkspace(ctx, admin.ID)
	collection, e := s.SaveCatalogCollection(owned, "", CatalogInput{Title: "旧草稿"})
	if e != nil {
		t.Fatal(e)
	}
	collection.Published = nil
	if e = s.Store.Update(owned, func(tx Tx) error { return recordSave(tx, "catalog_collection", collection.ID, collection) }); e != nil {
		t.Fatal(e)
	}
	store.failCatalogChange = true
	changed, e := s.ReconcileCatalogCollections(ctx)
	if !errors.Is(e, ErrUnauthorized) || changed != 0 {
		t.Fatal(changed, e)
	}
	after, e := s.AdminCatalogCollection(owned, collection.ID)
	if e != nil || after.Published != nil || after.Revision != collection.Revision {
		t.Fatal("partial migration committed", after, e)
	}
}
