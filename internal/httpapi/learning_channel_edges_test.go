package httpapi

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func TestChannelInvitesDoNotEnumerateAccounts(t *testing.T) {
	_, h := app()
	_, owner := managedLearner(t, h, "channel-invite-owner")
	reader, key := managedLearner(t, h, "channel-invite-known")
	var channel study.LearningChannel
	input := study.LearningChannelInput{Name: "invites", Visibility: "invited", SourceIDs: []string{}, InvitedEmails: []string{"not-yet-created@example.test", reader.Email}}
	w := request(t, h, "PUT", "/api/v1/workspace/channel", owner, input)
	data(t, w, &channel)
	if w.Code != 200 || strings.Contains(w.Body.String(), "user_id") || strings.Contains(w.Body.String(), reader.ID) {
		t.Fatal("invitation lookup disclosed account existence", w.Body.String())
	}
	// Unknown addresses are pending, not an error oracle; no account or session
	// is created, and no channel is available without an actual login.
	if len(channel.InvitedUsers) != 2 {
		t.Fatal("pending invite missing")
	}
	if w := request(t, h, "GET", "/api/v1/channels", key, nil); w.Code != 200 {
		t.Fatal("known invitation denied", w.Code)
	}
}

func TestChannelInvitationPinsIdentityAndAllowsPendingLogin(t *testing.T) {
	_, h := app()
	_, ownerKey := managedLearner(t, h, "invite-pin-owner")
	reader, _ := managedLearner(t, h, "invite-pin-reader")
	var channel study.LearningChannel
	input := study.LearningChannelInput{Name: "pinned invitation", Visibility: "invited", SourceIDs: []string{}, InvitedEmails: []string{reader.Email, "pending-invite@example.test"}}
	data(t, request(t, h, "PUT", "/api/v1/workspace/channel", ownerKey, input), &channel)
	oldEmail := reader.Email
	reader.Email = "invite-pin-renamed@example.test"
	var updated study.User
	data(t, request(t, h, "PUT", "/api/v1/admin/users/"+reader.ID, adminKey, map[string]any{"revision": reader.Revision, "email": reader.Email, "display_name": reader.DisplayName}), &updated)
	var session struct {
		Token string `json:"access_token"`
	}
	data(t, request(t, h, "POST", "/api/v1/user/auth/login", "", map[string]any{"email": reader.Email, "password": learnerPassword, "session_mode": "token"}), &session)
	replacement, replacementKey := managedLearner(t, h, "invite-pin-reader")
	if replacement.Email != oldEmail {
		t.Fatal("bad fixture")
	}
	input.Revision = channel.Revision
	data(t, request(t, h, "PUT", "/api/v1/workspace/channel", ownerKey, input), &channel)
	var listing struct {
		Items []study.LearningChannel `json:"items"`
	}
	data(t, request(t, h, "GET", "/api/v1/channels", session.Token, nil), &listing)
	if len(listing.Items) != 1 {
		t.Fatal("resaving channel lost stable invitation")
	}
	data(t, request(t, h, "GET", "/api/v1/channels", replacementKey, nil), &listing)
	if len(listing.Items) != 0 {
		t.Fatal("old email took over another account invitation")
	}
	_, pendingKey := managedLearner(t, h, "pending-invite")
	data(t, request(t, h, "GET", "/api/v1/channels", pendingKey, nil), &listing)
	if len(listing.Items) != 1 {
		t.Fatal("pending email invite did not require/survive actual login")
	}
}

func TestChannelPaginationPublicationAndOwnerRevocation(t *testing.T) {
	_, h := app()
	owner, ka := managedLearner(t, h, "channel-paged-owner")
	_, kb := managedLearner(t, h, "channel-paged-reader")
	var items []study.Item
	for i := 0; i < 3; i++ {
		var item study.Item
		data(t, request(t, h, "POST", "/api/v1/workspace/items", ka, map[string]any{"status": "pending", "content": map[string]any{"title": "visible lesson " + uuid.NewString(), "content": "published"}}), &item)
		data(t, request(t, h, "POST", "/api/v1/workspace/items/"+item.ID+"/publish", ka, map[string]any{"revision": item.Revision}), &item)
		items = append(items, item)
	}
	var unpublished study.Item
	data(t, request(t, h, "POST", "/api/v1/workspace/items", ka, map[string]any{"status": "draft", "content": map[string]any{"title": "never-public", "content": "draft secret"}}), &unpublished)
	var channel study.LearningChannel
	data(t, request(t, h, "PUT", "/api/v1/workspace/channel", ka, study.LearningChannelInput{Name: "paged", Visibility: "public", SourceIDs: []string{items[0].SourceID}}), &channel)
	path := "/api/v1/channels/" + channel.ID + "/items"
	var first, last study.ChannelItemPage
	data(t, request(t, h, "GET", path+"?limit=2", kb, nil), &first)
	if len(first.Items) != 2 || !first.HasMore || first.TotalIsExact || first.Total != -1 {
		t.Fatal("unbounded/full-count page", first)
	}
	data(t, request(t, h, "GET", path+"?limit=2&offset=2", kb, nil), &last)
	if len(last.Items) != 1 || last.HasMore || !last.TotalIsExact || last.Total != 3 {
		t.Fatal("last page contract", last)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, last.Items...) {
		if seen[item.ID] || item.ID == unpublished.ID {
			t.Fatal("duplicate/draft exposed")
		}
		seen[item.ID] = true
	}
	if w := request(t, h, "GET", path+"/"+unpublished.ID, kb, nil); w.Code != 404 {
		t.Fatal("draft readable", w.Code)
	}
	// Editing creates a newer private draft. The old published version is the
	// only view used by a visitor's reading and learning-position validation.
	original := items[0]
	var edited study.Item
	data(t, request(t, h, "PUT", "/api/v1/workspace/items/"+original.ID, ka, map[string]any{"revision": original.Revision, "status": "pending", "content": map[string]any{"title": "private new title", "content": "private new draft"}}), &edited)
	w := request(t, h, "GET", path+"/"+original.ID, kb, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private new") {
		t.Fatal("unpublished revision leaked", w.Body.String())
	}
	progress := map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": original.ID, "action": "progress", "mode": "preview", "expected_revision": 0, "position": map[string]any{"kind": "article", "version": original.Published.Revision, "block_id": "body", "offset": 1}}}}
	if w := request(t, h, "POST", "/api/v1/client/learning/events", kb, progress); w.Code != 200 {
		t.Fatal("published revision learning rejected", w.Code, w.Body.String())
	}
	var updated study.User
	data(t, request(t, h, "PUT", "/api/v1/admin/users/"+owner.ID, adminKey, map[string]any{"revision": owner.Revision, "email": owner.Email, "display_name": owner.DisplayName, "enabled": false}), &updated)
	if w := request(t, h, "GET", path, kb, nil); w.Code != 404 {
		t.Fatal("disabled publisher channel readable", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/client/learning/events", kb, map[string]any{"events": []any{map[string]any{"event_id": uuid.NewString(), "item_id": original.ID, "action": "view"}}}); w.Code != 404 {
		t.Fatal("disabled publisher new learning accepted", w.Code)
	}
}
