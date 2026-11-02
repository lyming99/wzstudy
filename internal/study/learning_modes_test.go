package study

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLearningModesQueriesAndReviewSchedule(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var store Store = NewMemoryStore()
			if kind == "file" {
				var err error
				store, err = OpenFileStore(filepath.Join(t.TempDir(), "study.json"))
				if err != nil {
					t.Fatal(err)
				}
			}
			defer store.Close()
			s := NewService(store)
			now := time.Date(2026, 10, 31, 4, 0, 0, 0, time.UTC)
			s.Now = func() time.Time { return now }
			_, key := feedbackClient(t, s, []string{"read", "feedback"})
			_, other := feedbackClient(t, s, []string{"read", "feedback"})
			_, reader := feedbackClient(t, s, []string{"read"})
			items := []Item{}
			for i := 0; i < 4; i++ {
				item, err := s.SaveManaged(ctx, "", ManagedInput{Status: "pending", Content: Content{Title: fmt.Sprintf("资讯%d", i), Content: "正文", Category: []string{"科技", "科技", "学习", ""}[i]}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				items = append(items, approveManaged(t, s, item))
				now = now.Add(time.Minute)
			}
			private := managedArticle(t, s, "draft", "不可见")
			choices, err := s.LearningModeCategories(ctx, key, "news", 100, 0)
			if err != nil || choices.Total != 1 || choices.Items[0].ID != "@uncategorized" || choices.Items[0].Count != 4 {
				t.Fatal(choices, err)
			}
			news, err := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "news", Category: "科技", Limit: 10})
			if err != nil || len(news.Items) != 4 || news.Items[0].ID != items[3].ID {
				t.Fatal(news, err)
			}
			unclassified, err := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "news", Category: "@uncategorized", Limit: 10})
			if err != nil || len(unclassified.Items) != 4 || unclassified.Items[0].ID != items[3].ID {
				t.Fatal(unclassified, err)
			}
			collection, err := s.SaveCatalogCollection(ctx, "", CatalogInput{Title: "课程合集", Chapters: []CatalogChapter{{Title: "第二条", ItemID: items[1].ID}, {Title: "第一条", ItemID: items[0].ID}}})
			if err != nil {
				t.Fatal(err)
			}
			collection, err = s.PublishCatalogCollection(ctx, collection.ID, collection.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			page, err := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "collection", CollectionID: collection.ID, Limit: 10})
			if err != nil || len(page.Items) != 2 || page.Items[0].ID != items[1].ID {
				t.Fatal(page, err)
			}
			root, branch := uuid.NewString(), uuid.NewString()
			roadmap, err := s.SaveLearningRoadmap(ctx, "", RoadmapInput{Title: "路线", Nodes: []RoadmapNode{{ID: branch, ParentID: root, Title: "分支", ItemIDs: []string{items[0].ID, private.ID}}, {ID: root, Title: "路线", ItemIDs: []string{items[2].ID}}}})
			if err != nil {
				t.Fatal(err)
			}
			page, err = s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "route", RoadmapID: roadmap.ID, Limit: 10})
			if err != nil || len(page.Items) != 2 || page.Items[0].ID != items[2].ID || page.Items[1].ID != items[0].ID {
				t.Fatal(page, err)
			}
			page, err = s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "route", RoadmapID: roadmap.ID, NodeID: branch, Limit: 10})
			if err != nil || len(page.Items) != 1 || page.Items[0].ID != items[0].ID {
				t.Fatal(page, err)
			}
			// A known read item comes after every unread item. A view arriving mid-round
			// cannot move the next page's boundary.
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[0].ID, Action: "view"}}); err != nil {
				t.Fatal(err)
			}
			random := LearningModeFilter{Mode: "random", Seed: 42, Limit: 100}
			all, err := s.LearningModeItems(ctx, key, random)
			if err != nil || all.Total != 4 || all.Items[3].ID != items[0].ID || len(all.UnreadItemIDs) != 3 {
				t.Fatal(all, err)
			}
			if _, err = s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: all.Items[0].ID, Action: "view"}}); err != nil {
				t.Fatal(err)
			}
			random.Offset, random.Limit = 1, 1
			next, err := s.LearningModeItems(ctx, key, random)
			if err != nil || next.Items[0].ID != all.Items[1].ID {
				t.Fatal(next, err)
			}
			// Custom intervals, exact review timestamp, idempotence and due queries.
			active := true
			_, err = s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[1].ID, Action: "review_plan", Value: &active, ReviewIntervalsMinutes: []int{5, 30}}})
			if err != nil {
				t.Fatal(err)
			}
			due := LearningModeFilter{Mode: "review", Limit: 10}
			page, err = s.LearningModeItems(ctx, key, due)
			if err != nil || page.Total != 1 {
				t.Fatal(page, err)
			}
			at := now.Add(-time.Minute)
			event := LearningEventInput{EventID: uuid.NewString(), ItemID: items[1].ID, Action: "review", OccurredAt: &at}
			result, err := s.ReportLearning(ctx, key, []LearningEventInput{event})
			if err != nil || result.States[0].LastReviewAt == nil || !result.States[0].LastReviewAt.Equal(at) || result.States[0].NextReviewAt == nil || !result.States[0].NextReviewAt.Equal(at.Add(5*time.Minute)) {
				t.Fatal(result, err)
			}
			repeated, err := s.ReportLearning(ctx, key, []LearningEventInput{event})
			if err != nil || repeated.Duplicates != 1 || !reflect.DeepEqual(repeated.States[0].NextReviewAt, result.States[0].NextReviewAt) {
				t.Fatal(repeated, err)
			}
			page, err = s.LearningModeItems(ctx, key, due)
			if err != nil || page.Total != 0 {
				t.Fatal(page, err)
			}
			// Re-adding an existing default plan must not pull its future due time forward.
			repeatedPlan, planErr := s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[1].ID, Action: "review_plan", Value: &active}})
			if planErr != nil || !repeatedPlan.States[0].NextReviewAt.Equal(*result.States[0].NextReviewAt) || repeatedPlan.States[0].ReviewStep != 1 {
				t.Fatal(repeatedPlan, planErr)
			}
			now = now.Add(4 * time.Minute)
			page, err = s.LearningModeItems(ctx, key, due)
			if err != nil || page.Total != 1 {
				t.Fatal(page, err)
			}
			page, err = s.LearningModeItems(ctx, other, due)
			if err != nil || page.Total != 0 {
				t.Fatal("another learner's review leaked", page, err)
			}
			if _, err = s.LearningModeItems(ctx, reader, due); !errors.Is(err, ErrForbidden) {
				t.Fatal("read-only client read review state", err)
			}
			// The next stage uses the user's second interval, rather than the first.
			second, secondErr := s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[1].ID, Action: "review"}})
			if secondErr != nil || !second.States[0].NextReviewAt.Equal(now.Add(30*time.Minute)) || second.States[0].ReviewStep != 2 {
				t.Fatal(second, secondErr)
			}
			// Omitting custom intervals uses the documented default schedule.
			_, defaultErr := s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[2].ID, Action: "review_plan", Value: &active}, {EventID: uuid.NewString(), ItemID: items[2].ID, Action: "review"}})
			if defaultErr != nil {
				t.Fatal(defaultErr)
			}
			defaults, defaultErr := s.ClientLearning(ctx, key, "", []string{items[2].ID}, 10, 0)
			if defaultErr != nil || !defaults.Items[0].NextReviewAt.Equal(now.Add(20*time.Minute)) {
				t.Fatal(defaults, defaultErr)
			}
			// Cancel both plans before the final empty-queue assertion.
			active = false
			_, _ = s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[2].ID, Action: "review_plan", Value: &active}})
			_, err = s.ReportLearning(ctx, key, []LearningEventInput{{EventID: uuid.NewString(), ItemID: items[1].ID, Action: "review_plan", Value: &active}})
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(24 * time.Hour)
			page, err = s.LearningModeItems(ctx, key, due)
			if err != nil || page.Total != 0 {
				t.Fatal(page, err)
			}
		})
	}
}

func TestContentLearningMetadataAndReviewValidation(t *testing.T) {
	source := Source{Name: "温知", Category: "学习", Language: "zh-CN"}
	content := Content{ExternalID: "video", Title: "课程", Kind: "video", URL: "https://www.bilibili.com/video/BVtest", DurationMS: 10000, Outline: "# 大纲", Chapters: []ContentChapter{{ID: "intro", Title: "简介", StartMS: 0}}, Subtitles: []ContentSubtitle{{ID: "zh", Language: "zh-CN", Format: "srt", Content: "1\n00:00:00,000 --> 00:00:01,000\n你好"}}}
	normalized, err := normalizeContent(content, source)
	if err != nil || normalized.Platform != "哔哩哔哩" || len(normalized.Subtitles) != 1 || normalized.Outline != "# 大纲" {
		t.Fatal(normalized, err)
	}
	content.URL = "https://evil.example/path/www.bilibili.com/video/test"
	if platform := contentPlatform(content, source.Name); platform != "evil.example" {
		t.Fatal("platform path was trusted", platform)
	}
	content.Chapters = append(content.Chapters, ContentChapter{ID: "duplicate", Title: "重复时间", StartMS: 0})
	if _, err = normalizeContent(content, source); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid chapters accepted", err)
	}
	for _, intervals := range [][]int{{0}, {-1}, {525601}, {}} {
		active := true
		_, err = normalizeLearningEvent(LearningEventInput{EventID: uuid.NewString(), ItemID: uuid.NewString(), Action: "review_plan", Value: &active, ReviewIntervalsMinutes: intervals}, time.Now())
		if !errors.Is(err, ErrInvalid) {
			t.Fatal("bad intervals accepted", intervals, err)
		}
	}
}

func TestRandomLearningQueriesBeyondRecommendationCandidateLimit(t *testing.T) {
	ctx := context.Background()
	s := NewService(NewMemoryStore())
	_, key := feedbackClient(t, s, []string{"read", "feedback"})
	sourceID := uuid.NewString()
	now := time.Now().UTC()
	err := s.Store.Update(ctx, func(tx Tx) error {
		if e := tx.SaveSource(Source{ID: sourceID, Slug: "all-items", Name: "全部资料", Kind: "manual", Enabled: true}); e != nil {
			return e
		}
		for i := 0; i < 1007; i++ {
			id := uuid.NewString()
			content := Content{ExternalID: fmt.Sprint(i), Title: fmt.Sprintf("资料%d", i), Kind: "article", Content: "正文"}
			item := Item{ID: id, SourceID: sourceID, ExternalID: content.ExternalID, Revision: 1, Draft: content, Published: &PublicItem{ID: id, SourceID: sourceID, Revision: 1, Content: content, UpdatedAt: now}, CreatedAt: now, UpdatedAt: now}
			if e := tx.SaveItem(item); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.LearningModeItems(ctx, key, LearningModeFilter{Mode: "random", Seed: 1, Offset: 1000, Limit: 100})
	if err != nil || page.Total != 1007 || len(page.Items) != 7 || page.HasMore {
		t.Fatal(page, err)
	}
}
