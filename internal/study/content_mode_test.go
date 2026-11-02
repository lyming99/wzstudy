package study

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestContentModeValidationAndLegacyHashCompatibility(t *testing.T) {
	source := Source{Category: "人工智能", Language: "zh"}
	legacy := Content{ExternalID: "legacy", Title: "旧资料", Content: "正文"}
	original, e := normalizeContent(legacy, source)
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(original)
	if e != nil || bytes.Contains(encoded, []byte("content_mode")) {
		t.Fatal(string(encoded), e)
	}
	explicit := legacy
	explicit.ContentMode = ContentModeDefault
	normalized, e := normalizeContent(explicit, source)
	if e != nil || normalized.ContentMode != "" || digest(normalized) != digest(original) {
		t.Fatal("default changed legacy content hash", normalized, e)
	}
	for _, mode := range []string{"default", "news", "learning"} {
		c := legacy
		c.ContentMode = mode
		c.Category = "主题保持不变"
		got, e := normalizeContent(c, source)
		if e != nil || effectiveContentMode(got.ContentMode) != mode || got.Category != c.Category {
			t.Fatal(got, e)
		}
	}
	for _, mode := range []string{"other", "NEWS", "资讯", "route"} {
		c := legacy
		c.ContentMode = mode
		if _, e = normalizeContent(c, source); !errors.Is(e, ErrInvalid) {
			t.Fatal(mode, e)
		}
	}
	if !MatchesContent(Content{}, Filter{ContentMode: "default"}) || MatchesContent(Content{ContentMode: "learning"}, Filter{ContentMode: "default"}) {
		t.Fatal("legacy mode list filtering failed")
	}
}

func TestContentModeMetadataIsIndependentAtomicAndPreservesPrivateDrafts(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var store Store = NewMemoryStore()
			if kind == "file" {
				var e error
				store, e = OpenFileStore(filepath.Join(t.TempDir(), "mode.json"))
				if e != nil {
					t.Fatal(e)
				}
			}
			defer store.Close()
			s := NewService(store)
			first := approveManaged(t, s, managedArticle(t, s, "pending", "公开标题"))
			second := approveManaged(t, s, managedArticle(t, s, "pending", "第二份资料"))
			content := first.Draft
			content.Title = "私密待审标题"
			content.Content = "私密待审正文"
			content.Category = "人工智能"
			pending, e := s.SaveManaged(ctx, first.ID, ManagedInput{Revision: first.Revision, SourceID: first.SourceID, Status: "pending", Content: content}, nil)
			if e != nil {
				t.Fatal(e)
			}
			learning := "learning"
			result, e := s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{pending.ID, pending.Revision}}, ContentMode: &learning})
			if e != nil || result.ModeUpdated != 1 || result.CategoryUpdated != 0 {
				t.Fatal(result, e)
			}
			after, e := s.Item(ctx, first.ID)
			if e != nil || after.Draft.ContentMode != "learning" || after.Published.ContentMode != "learning" || after.Draft.Category != pending.Draft.Category || after.Published.Category != first.Published.Category || after.Published.Title != first.Published.Title || after.Published.Content.Content != first.Published.Content.Content || after.Draft.Title != content.Title || ReviewState(after) != "pending" {
				t.Fatal("mode published pending body or overwrote category", after, e)
			}
			repeated, e := s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{after.ID, after.Revision}}, ContentMode: &learning})
			if e != nil || repeated.ModeUpdated != 0 || repeated.Items[0].Revision != after.Revision {
				t.Fatal("same mode not idempotent", repeated, e)
			}
			news := "news"
			_, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{after.ID, after.Revision}, {second.ID, second.Revision + 1}}, ContentMode: &news})
			if !errors.Is(e, ErrConflict) {
				t.Fatal("stale mode batch accepted", e)
			}
			unchanged, _ := s.Item(ctx, after.ID)
			if unchanged.Revision != after.Revision || unchanged.Published.ContentMode != "learning" {
				t.Fatal("mode batch partially committed", unchanged)
			}
			category := "资讯"
			result, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{after.ID, after.Revision}}, Category: &category})
			if e != nil || result.ModeUpdated != 0 || result.CategoryUpdated != 1 {
				t.Fatal(result, e)
			}
			unchanged, _ = s.Item(ctx, after.ID)
			if unchanged.Published.ContentMode != "learning" {
				t.Fatal("topic setter changed independent mode")
			}
			defaultMode := "default"
			result, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{unchanged.ID, unchanged.Revision}}, ContentMode: &defaultMode})
			if e != nil || result.ModeUpdated != 1 {
				t.Fatal(result, e)
			}
			cleared, _ := s.Item(ctx, after.ID)
			if cleared.Draft.ContentMode != "" || cleared.Published.ContentMode != "" || cleared.Draft.Category != "资讯" {
				t.Fatal("default mode reset changed topic", cleared)
			}
			invalid := "invalid"
			if _, e = s.OrganizeItems(ctx, ItemOrganizationInput{Items: []OrganizationItem{{cleared.ID, cleared.Revision}}, ContentMode: &invalid}); !errors.Is(e, ErrInvalid) {
				t.Fatal(e)
			}
		})
	}
}

func TestCollectorsPreserveAssignedModeUnlessExplicitlySupplied(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	enabled := true
	source, key, e := s.CreateSource(ctx, SourceInput{Slug: "content-mode", Name: "模式测试来源", Kind: "manual", Enabled: &enabled, Category: "人工智能", Permissions: []string{PermissionIngest}})
	if e != nil {
		t.Fatal(e)
	}
	content := Content{ExternalID: "stable-content", Title: "正文", Content: "第一版", ContentMode: "learning"}
	result, e := s.Ingest(ctx, source.ID, key, []Content{content})
	if e != nil || result.Created != 1 || result.Items[0].Published.ContentMode != "learning" {
		t.Fatal(result, e)
	}
	// Old collection payloads omit mode; they must not undo a management choice.
	content.ContentMode = ""
	content.Content = "第二版"
	result, e = s.Ingest(ctx, source.ID, key, []Content{content})
	if e != nil || result.Updated != 1 || result.Items[0].Draft.ContentMode != "learning" {
		t.Fatal(result, e)
	}
	// Older management clients likewise preserve an existing assignment.
	old := result.Items[0]
	draft := old.Draft
	draft.ContentMode = ""
	draft.Title = "管理端正文修改"
	managed, e := s.SaveManaged(ctx, old.ID, ManagedInput{Revision: old.Revision, SourceID: old.SourceID, Status: "pending", Content: draft}, nil)
	if e != nil || managed.Draft.ContentMode != "learning" {
		t.Fatal(managed, e)
	}
	content.ContentMode = "default"
	result, e = s.Ingest(ctx, source.ID, key, []Content{content})
	if e != nil || result.Updated != 1 || result.Items[0].Draft.ContentMode != "" {
		t.Fatal(result, e)
	}
	revision := result.Items[0].Revision
	content.ContentMode = ""
	result, e = s.Ingest(ctx, source.ID, key, []Content{content})
	if e != nil || result.Unchanged != 1 || result.Items[0].Revision != revision {
		t.Fatal("legacy replay changed revision", result, e)
	}
	invalid := content
	invalid.ExternalID = "bad"
	invalid.ContentMode = "invalid"
	if _, e = s.Ingest(ctx, source.ID, key, []Content{content, invalid}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	after, e := s.Item(ctx, old.ID)
	if e != nil || after.Revision != revision {
		t.Fatal("invalid batch committed", after, e)
	}
}
