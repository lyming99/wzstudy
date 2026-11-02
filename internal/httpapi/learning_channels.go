package httpapi

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"wzstudy/internal/study"
)

// Do not expose whether an invitation email currently resolves to an account.
func channelOwnerResponse(channel study.LearningChannel) study.LearningChannel {
	channel.InvitedUsers = append([]study.ChannelInvite(nil), channel.InvitedUsers...)
	for i := range channel.InvitedUsers {
		channel.InvitedUsers[i].UserID = ""
	}
	return channel
}

func (s *Server) ownLearningChannel(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.OwnLearningChannel(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, channelOwnerResponse(out))
}
func (s *Server) saveLearningChannel(w http.ResponseWriter, r *http.Request) {
	var in study.LearningChannelInput
	if !decodeLimit(w, r, &in, 128<<10) {
		return
	}
	out, err := s.service.SaveLearningChannel(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, channelOwnerResponse(out))
}
func (s *Server) learningChannels(w http.ResponseWriter, r *http.Request) {
	if !modeQueryValid(r) {
		s.fail(w, study.ErrInvalid)
		return
	}
	out, err := s.service.LearningChannels(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, map[string]any{"items": out})
}
func channelMediaURLs(item study.PublicItem, channelID string) study.PublicItem {
	item.Assets = append([]study.Asset(nil), item.Assets...)
	for i := range item.Assets {
		item.Assets[i].URL = "/api/v1/channels/" + channelID + "/items/" + item.ID + "/media/" + item.Assets[i].ID
	}
	return item
}
func (s *Server) learningChannelItems(w http.ResponseWriter, r *http.Request) {
	if !modeQueryValid(r, "limit", "offset", "q", "kind") {
		s.fail(w, study.ErrInvalid)
		return
	}
	q := r.URL.Query()
	limit, e := parseInt(q.Get("limit"), 12)
	if e != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	offset, e := parseInt(q.Get("offset"), 0)
	if e != nil {
		s.fail(w, study.ErrInvalid)
		return
	}
	kind := q.Get("kind")
	if kind != "" && kind != "article" && kind != "audio" && kind != "video" && kind != "carousel" {
		s.fail(w, study.ErrInvalid)
		return
	}
	id := chi.URLParam(r, "channel_id")
	out, e := s.service.LearningChannelItems(r.Context(), id, study.Filter{Limit: limit, Offset: offset, Query: q.Get("q"), Kind: kind})
	if e != nil {
		s.fail(w, e)
		return
	}
	for i := range out.Items {
		out.Items[i] = channelMediaURLs(out.Items[i], id)
	}
	success(w, 200, out)
}
func (s *Server) learningChannelItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "channel_id")
	out, e := s.service.LearningChannelItem(r.Context(), id, chi.URLParam(r, "item_id"))
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, channelMediaURLs(out, id))
}
func (s *Server) learningChannelMedia(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "asset_id")
	if _, e := uuid.Parse(id); e != nil {
		s.fail(w, study.ErrNotFound)
		return
	}
	asset, e := s.service.LearningChannelAsset(r.Context(), chi.URLParam(r, "channel_id"), chi.URLParam(r, "item_id"), id)
	if e != nil {
		s.fail(w, e)
		return
	}
	f, e := os.Open(filepath.Join(s.cfg.DataDir, "uploads", asset.ID))
	if e != nil {
		s.fail(w, study.ErrNotFound)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		s.fail(w, e)
		return
	}
	w.Header().Set("Content-Type", asset.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": asset.Name}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", "\""+asset.ID+"\"")
	http.ServeContent(w, r, asset.Name, info.ModTime(), f)
}
