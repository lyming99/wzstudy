package study

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"wzstudy/internal/auth"
)

// A channel shares published content, never credentials, drafts or learning
// records. There is exactly one stable channel ID per workspace (the owner ID).
// "public" means discoverable by authenticated users, not anonymous access.
type ChannelInvite struct {
	UserID string `json:"user_id,omitempty"`
	Email  string `json:"email"`
}
type LearningChannel struct {
	ID           string          `json:"id"`
	OwnerID      string          `json:"owner_id,omitempty"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Visibility   string          `json:"visibility"`
	SourceIDs    []string        `json:"source_ids,omitempty"`
	InvitedUsers []ChannelInvite `json:"invited_users,omitempty"`
	Revision     int64           `json:"revision"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
type LearningChannelInput struct {
	Revision      int64    `json:"revision"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Visibility    string   `json:"visibility"`
	SourceIDs     []string `json:"source_ids"`
	InvitedEmails []string `json:"invited_emails"`
}
type ChannelItemPage struct {
	Items        []PublicItem `json:"items"`
	Total        int          `json:"total"`
	Limit        int          `json:"limit"`
	Offset       int          `json:"offset"`
	HasMore      bool         `json:"has_more"`
	TotalIsExact bool         `json:"total_is_exact"`
}

type sharedLearningContextKey struct{}

// WithSharedLearning is only set after user-session authentication on personal
// learning endpoints. It does not change the record namespace, write authority,
// source access, catalog queries or sync cursors of that user's workspace.
func WithSharedLearning(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedLearningContextKey{}, true)
}
func channelBaseTx(tx Tx) Tx {
	if scoped, ok := tx.(*workspaceTx); ok {
		return scoped.Tx
	}
	return tx
}
func channelForOwner(tx Tx, owner string) (LearningChannel, error) {
	base := channelBaseTx(tx)
	profile, err := workspaceUserByID(base, owner)
	if err != nil {
		return LearningChannel{}, err
	}
	if !profile.Enabled {
		return LearningChannel{}, ErrNotFound
	}
	owned := &workspaceTx{Tx: base, owner: owner}
	channel, err := recordAs[LearningChannel](owned, "learning_channel", "singleton")
	if errors.Is(err, ErrNotFound) {
		return LearningChannel{ID: owner, OwnerID: owner, Name: profile.DisplayName + "的学习渠道", Visibility: "private", SourceIDs: []string{}, InvitedUsers: []ChannelInvite{}}, nil
	}
	if err != nil {
		return LearningChannel{}, err
	}
	if channel.ID != owner || channel.OwnerID != owner {
		return LearningChannel{}, ErrNotFound
	}
	return channel, nil
}
func channelAllows(tx Tx, channel LearningChannel, viewer string) bool {
	if viewer == "" {
		return false
	}
	if viewer == channel.OwnerID {
		return true
	}
	if channel.Visibility == "public" {
		return true
	}
	if channel.Visibility == "invited" {
		profile, err := workspaceUserByID(channelBaseTx(tx), viewer)
		if err != nil || !profile.Enabled {
			return false
		}
		for _, invite := range channel.InvitedUsers {
			// Existing accounts are pinned to stable IDs. Pending invitations
			// become usable only after an actual account login with that email.
			if invite.UserID == viewer || (invite.UserID == "" && invite.Email == profile.Email) {
				return true
			}
		}
	}
	return false
}
func channelHasSource(channel LearningChannel, id string) bool {
	// Empty selection shares NOTHING; never reuse MatchesSource's all-sources
	// wildcard here. Newly added sources are not silently made public.
	for _, sourceID := range channel.SourceIDs {
		if sourceID == id {
			return true
		}
	}
	return false
}
func channelPublishedItem(tx Tx, channel LearningChannel, viewer, itemID string) (Item, error) {
	if !channelAllows(tx, channel, viewer) {
		return Item{}, ErrNotFound
	}
	base := channelBaseTx(tx)
	item, err := base.Item(itemID)
	if err != nil {
		return Item{}, err
	}
	if item.OwnerID != channel.OwnerID || item.DeletedAt != nil || item.Published == nil || !channelHasSource(channel, item.SourceID) {
		return Item{}, ErrNotFound
	}
	source, err := base.Source(item.SourceID)
	if err != nil {
		return Item{}, err
	}
	if source.OwnerID != channel.OwnerID || !source.Enabled {
		return Item{}, ErrNotFound
	}
	// Do not expose an unpublished newer draft through the learning transaction.
	item.Draft = item.Published.Content
	item.Revision = item.Published.Revision
	item.History = nil
	item.ContentHash = ""
	return item, nil
}
func sharedLearningItem(tx *workspaceTx, item Item) (Item, error) {
	channel, err := channelForOwner(tx, item.OwnerID)
	if err != nil {
		return Item{}, err
	}
	return channelPublishedItem(tx, channel, tx.owner, item.ID)
}

func (s *Service) OwnLearningChannel(ctx context.Context) (out LearningChannel, err error) {
	owner := WorkspaceUserID(ctx)
	if owner == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = channelForOwner(tx, owner); return e })
	return
}
func (s *Service) SaveLearningChannel(ctx context.Context, in LearningChannelInput) (out LearningChannel, err error) {
	owner := WorkspaceUserID(ctx)
	if owner == "" {
		return out, ErrUnauthorized
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Revision < 0 || in.Name == "" || !validText(in.Name, 200) || !validText(in.Description, 4000) || len(in.SourceIDs) > 1000 || len(in.InvitedEmails) > 100 || (in.Visibility != "private" && in.Visibility != "public" && in.Visibility != "invited") {
		return out, ErrInvalid
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		current, e := channelForOwner(tx, owner)
		if e != nil {
			return e
		}
		if current.Revision != in.Revision {
			return ErrConflict
		}
		selected := map[string]bool{}
		sources := []string{}
		for _, id := range in.SourceIDs {
			normalized, ok := validCatalogID(id)
			if !ok || selected[normalized] {
				return ErrInvalid
			}
			source, e := tx.Source(normalized)
			if e != nil {
				return e
			}
			if source.OwnerID != owner {
				return ErrNotFound
			}
			selected[normalized] = true
			sources = append(sources, normalized)
		}
		sort.Strings(sources)
		profiles, e := recordsAs[userRecord](tx, "learning_user")
		if e != nil {
			return e
		}
		byEmail := map[string]User{}
		for _, record := range profiles {
			if record.Profile.Enabled {
				byEmail[record.Profile.Email] = record.Profile
			}
		}
		admin, e := readAdministrator(tx)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		if e == nil {
			profile, e := workspaceUserByID(tx, admin.Profile.ID)
			if e == nil && profile.Enabled {
				byEmail[profile.Email] = profile
			}
		}
		invites := []ChannelInvite{}
		previousInvites := map[string]ChannelInvite{}
		for _, invite := range current.InvitedUsers {
			previousInvites[invite.Email] = invite
		}
		seen := map[string]bool{}
		for _, raw := range in.InvitedEmails {
			email, e := auth.NormalizeEmail(raw)
			if e != nil {
				return ErrInvalid
			}
			profile := byEmail[email]
			if seen[email] {
				return ErrInvalid
			}
			seen[email] = true
			// Nonexistent/disabled emails are accepted as pending invitations;
			// HTTP responses never reveal resolution IDs or account existence.
			userID := profile.ID
			// Saving an unchanged invitation must not rebind it to a different
			// account after the original invitee changes their email.
			if previous := previousInvites[email]; previous.UserID != "" {
				userID = previous.UserID
			}
			invites = append(invites, ChannelInvite{UserID: userID, Email: email})
		}
		sort.Slice(invites, func(i, j int) bool { return invites[i].Email < invites[j].Email })
		out = LearningChannel{ID: owner, OwnerID: owner, Name: in.Name, Description: in.Description, Visibility: in.Visibility, SourceIDs: sources, InvitedUsers: invites, Revision: current.Revision + 1, UpdatedAt: s.Now().UTC()}
		return recordSave(tx, "learning_channel", "singleton", out)
	})
	if err != nil {
		out = LearningChannel{}
	}
	return
}
func (s *Service) LearningChannels(ctx context.Context) (out []LearningChannel, err error) {
	viewer := WorkspaceUserID(ctx)
	out = []LearningChannel{}
	if viewer == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		raw, e := channelBaseTx(tx).Records("learning_channel")
		if e != nil {
			return e
		}
		for _, record := range raw {
			var channel LearningChannel
			if e := json.Unmarshal(record, &channel); e != nil {
				return e
			}
			if channel.OwnerID == viewer || recordOwner(record) != channel.OwnerID {
				continue
			}
			// Re-check that the account is still enabled; a disabled publisher cannot
			// leave an anonymously owned or orphaned channel readable.
			active, e := channelForOwner(tx, channel.OwnerID)
			if errors.Is(e, ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			if !channelAllows(tx, active, viewer) {
				continue
			}
			active.OwnerID = ""
			active.SourceIDs = nil
			active.InvitedUsers = nil
			out = append(out, active)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Name == out[j].Name {
				return out[i].ID < out[j].ID
			}
			return out[i].Name < out[j].Name
		})
		return nil
	})
	return
}
func (s *Service) LearningChannelItems(ctx context.Context, channelID string, f Filter) (out ChannelItemPage, err error) {
	viewer := WorkspaceUserID(ctx)
	out = ChannelItemPage{Items: []PublicItem{}, Limit: f.Limit, Offset: f.Offset, TotalIsExact: true}
	if viewer == "" {
		return out, ErrUnauthorized
	}
	if _, ok := validCatalogID(channelID); !ok || f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 1000000 || !validText(f.Query, 500) {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		channel, e := channelForOwner(tx, channelID)
		if e != nil {
			return e
		}
		if !channelAllows(tx, channel, viewer) {
			return ErrNotFound
		}
		if len(channel.SourceIDs) == 0 {
			return nil
		}
		base := channelBaseTx(tx)
		enabled := []string{}
		for _, id := range channel.SourceIDs {
			source, e := base.Source(id)
			if errors.Is(e, ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			if source.OwnerID == channel.OwnerID && source.Enabled {
				enabled = append(enabled, id)
			}
		}
		if len(enabled) == 0 {
			return nil
		}
		filter := f
		filter.OwnerID = channel.OwnerID
		filter.SourceIDs = enabled
		filter.Admin = false
		filter.Limit = f.Limit + 1
		filter.Offset = f.Offset
		items, e := base.Items(filter)
		if e != nil {
			return e
		}
		out.HasMore = len(items) > f.Limit
		if out.HasMore {
			items = items[:f.Limit]
		}
		for _, item := range items {
			// The scoped SQL/memory filter already enforces published, live
			// records and selected enabled sources in this same snapshot.
			if item.OwnerID != channel.OwnerID || item.DeletedAt != nil || item.Published == nil {
				return ErrNotFound
			}
			out.Items = append(out.Items, *item.Published)
		}
		out.TotalIsExact = !out.HasMore && (len(items) > 0 || f.Offset == 0)
		if out.TotalIsExact {
			out.Total = f.Offset + len(items)
		} else {
			out.Total = -1
		}

		return nil
	})
	return
}
func (s *Service) LearningChannelItem(ctx context.Context, channelID, itemID string) (out PublicItem, err error) {
	viewer := WorkspaceUserID(ctx)
	if viewer == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		channel, e := channelForOwner(tx, channelID)
		if e != nil {
			return e
		}
		item, e := channelPublishedItem(tx, channel, viewer, itemID)
		if e != nil {
			return e
		}
		out = *item.Published
		return nil
	})
	return
}

// Media access requires the channel, item and exact published asset reference.
// An asset registry entry or a draft-only reference never grants access.
func (s *Service) LearningChannelAsset(ctx context.Context, channelID, itemID, assetID string) (out Asset, err error) {
	item, err := s.LearningChannelItem(ctx, channelID, itemID)
	if err != nil {
		return out, err
	}
	for _, asset := range item.Assets {
		if asset.ID == assetID {
			return asset, nil
		}
	}
	return out, ErrNotFound
}
