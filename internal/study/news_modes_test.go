package study

import (
	"context"
	"testing"
)

func TestNewsUsesContentModeAndNeverTopicCategory(t *testing.T) {
	s := NewService(NewMemoryStore())
	ctx := context.Background()
	_, key := feedbackClient(t, s, []string{"read", "feedback"})
	expected := map[string]bool{}
	fixtures := []struct {
		mode, category string
		news           bool
	}{
		{"", "人工智能", true}, {"default", "AI创作", true}, {"news", "设计灵感", true},
		{"learning", "资讯", false}, {"learning", "", false},
	}
	for i, f := range fixtures {
		item, e := s.SaveManaged(ctx, "", ManagedInput{Status: "pending", Content: Content{Title: []string{"legacy", "default", "news", "learning-topic-news", "learning-empty-topic"}[i], Category: f.category, ContentMode: f.mode, Content: "正文"}}, nil)
		if e != nil {
			t.Fatal(e)
		}
		item = approveManaged(t, s, item)
		if f.news {
			expected[item.ID] = true
		}
	}
	if _, e := s.SaveManaged(ctx, "", ManagedInput{Status: "pending", Content: Content{Title: "未公开资讯", ContentMode: "news", Content: "私密正文"}}, nil); e != nil {
		t.Fatal(e)
	}
	for _, legacyCategory := range []string{"", "@uncategorized", "资讯", "人工智能"} {
		page, e := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "news", Category: legacyCategory, Limit: 100})
		if e != nil || page.Total != 3 || len(page.Items) != 3 {
			t.Fatal("content-mode query or private draft filtering failed", page, e)
		}
		for _, item := range page.Items {
			if !expected[item.ID] {
				t.Fatal("learning mode leaked into news", item)
			}
		}
	}
	page, e := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "random", Limit: 100})
	if e != nil || page.Total != 5 {
		t.Fatal("random mode was narrowed by content mode", page, e)
	}
	choices, e := s.LearningModeCategories(ctx, key, "news", 100, 0)
	if e != nil || choices.Total != 1 || choices.Items[0].Count != 3 {
		t.Fatal(choices, e)
	}
}
