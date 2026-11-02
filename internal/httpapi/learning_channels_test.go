package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestLearningChannelAccessAndPersonalIsolation(t *testing.T) {
	_, h := app()
	a, ka := managedLearner(t, h, "channel-owner")
	b, kb := managedLearner(t, h, "channel-reader")
	_, kc := managedLearner(t, h, "channel-outsider")
	var item study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items", ka, map[string]any{"status": "pending", "content": map[string]any{"title": "shared lesson", "content": "published text"}}), &item)
	data(t, request(t, h, "POST", "/api/v1/workspace/items/"+item.ID+"/publish", ka, map[string]any{"revision": item.Revision}), &item)
	var own study.LearningChannel
	data(t, request(t, h, "GET", "/api/v1/workspace/channel", ka, nil), &own)
	if own.ID != a.ID || own.Visibility != "private" || own.Revision != 0 {
		t.Fatal("unsafe default", own)
	}
	save := func(visibility string, emails []string) {
		t.Helper()
		data(t, request(t, h, "PUT", "/api/v1/workspace/channel", ka, study.LearningChannelInput{Revision: own.Revision, Name: "owner channel", Visibility: visibility, SourceIDs: []string{item.SourceID}, InvitedEmails: emails}), &own)
	}
	path := "/api/v1/channels/" + own.ID + "/items"
	if w := request(t, h, "GET", path, kb, nil); w.Code != 404 {
		t.Fatal("private channel readable", w.Code)
	}
	save("invited", []string{b.Email})
	for _, key := range []string{"", adminKey, readKey} {
		if w := request(t, h, "GET", path, key, nil); w.Code != 401 {
			t.Fatal("non-user channel access", w.Code)
		}
	}
	if w := request(t, h, "GET", path, kc, nil); w.Code != 404 {
		t.Fatal("uninvited channel readable", w.Code)
	}
	var page study.ChannelItemPage
	data(t, request(t, h, "GET", path, kb, nil), &page)
	if page.Total != 1 || page.Items[0].ID != item.ID {
		t.Fatal("shared items", page)
	}
	// Sharing does not broaden the recipient's own library or management APIs.
	var privatePage struct {
		Items []study.PublicItem `json:"items"`
	}
	data(t, request(t, h, "GET", "/api/v1/items", kb, nil), &privatePage)
	if len(privatePage.Items) != 0 {
		t.Fatal("shared item leaked into own catalog")
	}
	if w := request(t, h, "GET", "/api/v1/workspace/items/"+item.ID, kb, nil); w.Code != 404 {
		t.Fatal("shared draft readable", w.Code)
	}
	channels := request(t, h, "GET", "/api/v1/channels", kb, nil)
	if channels.Code != 200 || strings.Contains(channels.Body.String(), b.Email) || strings.Contains(channels.Body.String(), "invited_users") || strings.Contains(channels.Body.String(), "source_ids") {
		t.Fatal("channel ACL leaked", channels.Body.String())
	}
	event := map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "view"}}}
	if w := request(t, h, "POST", "/api/v1/client/learning/events", kb, event); w.Code != 200 {
		t.Fatal("shared learning failed", w.Code, w.Body.String())
	}
	var learning study.LearningPage
	data(t, request(t, h, "GET", "/api/v1/client/learning?item_id="+item.ID, kb, nil), &learning)
	if learning.ClientID != b.ID || learning.Total != 1 || learning.Items[0].ViewCount != 1 {
		t.Fatal("wrong learner", learning)
	}
	data(t, request(t, h, "GET", "/api/v1/client/learning?item_id="+item.ID, ka, nil), &learning)
	if learning.Total != 0 {
		t.Fatal("recipient learning written to publisher")
	}
	save("public", nil)
	data(t, request(t, h, "GET", path, kc, nil), &page)
	save("private", nil)
	if w := request(t, h, "GET", path, kb, nil); w.Code != 404 {
		t.Fatal("revoked channel remains readable", w.Code)
	}
	event = map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": item.ID, "action": "view"}}}
	if w := request(t, h, "POST", "/api/v1/client/learning/events", kb, event); w.Code != 404 {
		t.Fatal("revoked channel learning accepted", w.Code)
	}
}

func TestChannelEmptySelectionCSRFAndRevision(t *testing.T) {
	_, h := app()
	a, key := managedLearner(t, h, "channel-settings")
	in := study.LearningChannelInput{Revision: 0, Name: "empty public", Visibility: "public", SourceIDs: []string{}, InvitedEmails: []string{}}
	var channel study.LearningChannel
	data(t, request(t, h, "PUT", "/api/v1/workspace/channel", key, in), &channel)
	if w := request(t, h, "PUT", "/api/v1/workspace/channel", key, in); w.Code != 409 {
		t.Fatal("stale channel accepted", w.Code)
	}
	_, other := managedLearner(t, h, "channel-empty-reader")
	var page study.ChannelItemPage
	data(t, request(t, h, "GET", "/api/v1/channels/"+a.ID+"/items", other, nil), &page)
	if page.Total != 0 {
		t.Fatal("empty selection is wildcard")
	}
	login := request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": a.Email, "password": learnerPassword})
	var cookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == userSessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing cookie")
	}
	if w := cookieRequest(t, h, "PUT", "/api/v1/workspace/channel", "", cookie, "", in); w.Code != 403 {
		t.Fatal("channel CSRF", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/channels?owner_id="+a.ID, key, nil); w.Code != 400 {
		t.Fatal("owner spoof accepted", w.Code)
	}
}

func TestAudioSubtitleAndChapterIngestion(t *testing.T) {
	svc, h := app()
	a, _ := managedLearner(t, h, "audio-cues")
	ctx := study.WithWorkspace(context.Background(), a.ID)
	item, err := svc.SaveManaged(ctx, "", study.ManagedInput{Status: "pending", Content: study.Content{Title: "audio lesson", Kind: "audio", URL: "https://example.test/audio", DurationMS: 10000, Chapters: []study.ContentChapter{{ID: "intro", Title: "intro", StartMS: 0}}, Subtitles: []study.ContentSubtitle{{ID: "en", Language: "en", Format: "srt", Content: "1\n00:00:00,000 --> 00:00:02,000\nHello"}, {ID: "zh", Language: "zh-CN", Format: "vtt", Content: "WEBVTT\n\n00:00.000 --> 00:02.000\n你好"}}}}, nil)
	if err != nil || len(item.Draft.Subtitles) != 2 {
		t.Fatal("audio subtitles rejected", err)
	}
}
