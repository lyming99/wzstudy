package study

import (
	"context"
	"testing"
)

func TestWorkspaceSourceDoesNotCollideOrBecomeClient(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	ordinary, _, e := s.CreateSource(ctx, SourceInput{Slug: "studio-workspace", Name: "已有来源", Kind: "manual"})
	if e != nil {
		t.Fatal(e)
	}
	item := managedArticle(t, s, "draft", "保留已有来源的兼容性")
	if item.SourceID == ordinary.ID {
		t.Fatal("workspace reused a client source")
	}
	internal, e := s.Source(ctx, item.SourceID)
	if e != nil || !internal.System || internal.Slug == ordinary.Slug {
		t.Fatal(internal, e)
	}
	if _, e = s.UpdateSource(ctx, internal.ID, internal.Revision, SourceInput{Slug: internal.Slug, Kind: internal.Kind, Name: "不能编辑内部来源"}); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, key, e := s.RotateKey(ctx, internal.ID, internal.Revision); e != ErrInvalid || key != "" {
		t.Fatal("internal source acquired client credentials", e)
	}
	if _, e = s.Ingest(ctx, internal.ID, "arbitrary-source-key", []Content{{ExternalID: "wrong", Title: "不能作为客户端入库"}}); e != ErrUnauthorized {
		t.Fatal(e)
	}
}
