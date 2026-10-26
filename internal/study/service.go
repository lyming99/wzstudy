package study

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	Store Store
	Now   func() time.Time
}

func NewService(s Store) *Service {
	return &Service{Store: s, Now: func() time.Time { return time.Now().UTC() }}
}
func (s *Service) Sources(ctx context.Context) (out []Source, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = tx.Sources(); return e })
	for i := range out {
		out[i] = sourcePolicy(out[i])
	}
	return
}
func (s *Service) Source(ctx context.Context, id string) (out Source, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = tx.Source(id); return e })
	out = sourcePolicy(out)
	return
}

func (s *Service) CreateSource(ctx context.Context, in SourceInput) (out Source, key string, err error) {
	in, err = normalizeSource(in)
	if err != nil {
		return
	}
	key, err = NewKey()
	if err != nil {
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if in.Kind == "manual" && in.Integration == "" {
		in.Integration = "collector"
	}
	now := s.Now()
	out = Source{ID: uuid.NewString(), Slug: in.Slug, Name: in.Name, Description: in.Description, Kind: in.Kind, Integration: in.Integration, FeedURL: in.FeedURL, Category: in.Category, Language: in.Language, Enabled: enabled, AutoPublish: in.AutoPublish, Permissions: in.Permissions, PollIntervalSeconds: in.PollIntervalSeconds, Revision: 1, CreatedAt: now, UpdatedAt: now, NextPollAt: now, KeyHash: HashKey(key)}
	err = s.Store.Update(ctx, func(tx Tx) error { return tx.SaveSource(out) })
	if err != nil {
		key = ""
	}
	return
}

// UpdateSource is a full replacement, guarded by the source revision. In-flight
// fetches are invalidated even if only policy or key settings change.
func (s *Service) UpdateSource(ctx context.Context, id string, rev int64, in SourceInput) (out Source, err error) {
	in, err = normalizeSource(in)
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		old, e := tx.Source(id)
		if e != nil {
			return e
		}
		if old.Revision != rev {
			return ErrConflict
		}
		if len(legacyPolicy(old, []string{PermissionRead, PermissionSync})) > 0 {
			return ErrConflict
		}
		if old.System || old.Kind != in.Kind || old.Slug != in.Slug {
			return ErrInvalid
		}
		if in.Integration != "" {
			old.Integration = in.Integration
		}
		old.Name = in.Name
		old.Description = in.Description
		old.Category = in.Category
		old.Language = in.Language
		if old.FeedURL != in.FeedURL {
			old.ETag = ""
			old.LastModified = ""
		}
		old.FeedURL = in.FeedURL
		if in.Enabled != nil {
			old.Enabled = *in.Enabled
		}
		old.AutoPublish = in.AutoPublish
		// Older clients omit this field on updates. Preserve an explicit policy;
		// only an explicit [] revokes every capability.
		if in.Permissions != nil {
			old.Permissions = in.Permissions
		}
		old.PollIntervalSeconds = in.PollIntervalSeconds
		old.PollToken = ""
		old.PollUntil = time.Time{}
		old.NextPollAt = s.Now()
		old.Revision++
		old.UpdatedAt = s.Now()
		out = sourcePolicy(old)
		return tx.SaveSource(old)
	})
	return
}

func (s *Service) RotateKey(ctx context.Context, id string, rev int64) (out Source, key string, err error) {
	key, err = NewKey()
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Source(id)
		if e != nil {
			return e
		}
		if v.Revision != rev {
			return ErrConflict
		}
		if v.System {
			return ErrInvalid
		}
		if len(legacyPolicy(v, []string{PermissionRead, PermissionSync})) > 0 {
			return ErrConflict
		}
		v.KeyHash = HashKey(key)
		v.Revision++
		v.UpdatedAt = s.Now()
		out = sourcePolicy(v)
		return tx.SaveSource(v)
	})
	if err != nil {
		key = ""
	}
	return
}

func (s *Service) Ingest(ctx context.Context, id, key string, inputs []Content) (out IngestResult, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		source, e := sourceAccess(tx, id, key, PermissionIngest)
		if e != nil {
			return e
		}
		out, e = s.ingest(tx, source, inputs)
		return e
	})
	return
}

func (s *Service) ingest(tx Tx, source Source, inputs []Content) (out IngestResult, err error) {
	if len(inputs) < 1 || len(inputs) > 100 {
		return out, ErrInvalid
	}
	// Validate the entire batch before writing, including duplicate external IDs.
	normalized := make([]Content, len(inputs))
	seen := map[string]bool{}
	for i, v := range inputs {
		v, e := normalizeContent(v, source)
		if e != nil {
			return out, e
		}
		if e = normalizeAssets(tx, &v, source.ID, false); e != nil {
			return out, e
		}
		if seen[v.ExternalID] {
			return out, ErrInvalid
		}
		seen[v.ExternalID] = true
		normalized[i] = v
	}
	out.Items = make([]Item, 0, len(inputs))
	for _, content := range normalized {
		old, e := tx.FindItem(source.ID, content.ExternalID)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return out, e
		}
		if e == nil && old.DeletedAt != nil {
			return out, ErrConflict
		}
		if e == nil && old.ContentHash == digest(content) {
			out.Unchanged++
			out.Items = append(out.Items, old)
			continue
		}
		now := s.Now()
		if errors.Is(e, ErrNotFound) {
			old = Item{ID: uuid.NewString(), SourceID: source.ID, ExternalID: content.ExternalID, CreatedAt: now}
			out.Created++
		} else {
			out.Updated++
		}
		old.Status = "pending"
		audit(&old, "客户端或订阅内容已入库", now)
		old.Draft = content
		old.ContentHash = digest(content)
		old.Revision++
		old.UpdatedAt = now
		if source.AutoPublish {
			if e = s.publish(tx, &old, true); e != nil {
				return out, e
			}
			out.Published++
		}
		if e = tx.SaveItem(old); e != nil {
			return out, e
		}
		out.Items = append(out.Items, old)
	}
	receipt, e := s.recordIntake(tx, source, out)
	if e != nil {
		return out, e
	}
	out.ReceiptID, out.SourceID, out.ReceivedAt = receipt.ID, source.ID, receipt.ReceivedAt
	return
}

func (s *Service) publish(tx Tx, v *Item, publish bool) error {
	now := s.Now()
	var live *PublicItem
	op := "delete"
	if publish {
		c := v.Draft
		if c.PublishedAt == nil {
			t := v.CreatedAt
			c.PublishedAt = &t
		}
		live = &PublicItem{ID: v.ID, SourceID: v.SourceID, Revision: v.Revision, Content: c, UpdatedAt: now}
		op = "upsert"
	}
	if publish {
		v.Status = "published"
		audit(v, "审核通过并发布", now)
	} else {
		v.Status = "draft"
		audit(v, "资料已下架", now)
	}
	v.Published = live
	_, err := tx.Append(Change{Operation: op, ItemID: v.ID, SourceID: v.SourceID, Revision: v.Revision, Item: live, CreatedAt: now})
	return err
}

func (s *Service) Publication(ctx context.Context, id string, rev int64, publish bool) (out Item, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Item(id)
		if e != nil {
			return e
		}
		if v.Revision != rev {
			return ErrConflict
		}
		if v.DeletedAt != nil {
			return ErrNotFound
		}
		if publish && ReviewState(v) == "draft" {
			return ErrInvalid
		}
		// Repeated approval/removal is a no-op, not another feed event.
		if (!publish && v.Published == nil) || (publish && v.Published != nil && digest(v.Published.Content) == digest(publishedContent(v))) {
			if publish && v.Status != "published" {
				v.Status = "published"
				v.Revision++
				v.UpdatedAt = s.Now()
				audit(&v, "审核通过（公开内容未变化）", s.Now())
				if e = tx.SaveItem(v); e != nil {
					return e
				}
			}
			out = v
			return nil
		}
		v.Revision++
		v.UpdatedAt = s.Now()
		if e = s.publish(tx, &v, publish); e != nil {
			return e
		}
		if e = tx.SaveItem(v); e != nil {
			return e
		}
		out = v
		return nil
	})
	return
}
func publishedContent(v Item) Content {
	c := v.Draft
	if c.PublishedAt == nil {
		t := v.CreatedAt
		c.PublishedAt = &t
	}
	return c
}

func (s *Service) Items(ctx context.Context, f Filter) (out []Item, err error) {
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 10000 || len(f.SourceIDs) > 20 || len(f.Query) > 200 {
		return nil, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = tx.Items(f); return e })
	return
}
func (s *Service) Item(ctx context.Context, id string) (out Item, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		out, e = tx.Item(id)
		if e == nil && out.DeletedAt != nil {
			return ErrNotFound
		}
		return e
	})
	return
}
func (s *Service) Changes(ctx context.Context, after int64, limit int, ids []string) (out ChangePage, err error) {
	if after < 0 || limit < 1 || limit > 100 || len(ids) > 20 {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = tx.Changes(after, limit, ids); return e })
	return
}

// ClaimPoll grants a short lease. Updating a source invalidates this token;
// multiple API processes may poll safely without committing obsolete results.
func (s *Service) ClaimPoll(ctx context.Context, id string, force bool) (out Source, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Source(id)
		if e != nil {
			return e
		}
		now := s.Now()
		if !v.Enabled {
			return ErrDisabled
		}
		if v.Kind != "rss" {
			return ErrInvalid
		}
		if v.PollUntil.After(now) || (!force && v.NextPollAt.After(now)) {
			return ErrBusy
		}
		v.PollToken = uuid.NewString()
		v.PollUntil = now.Add(90 * time.Second)
		v.NextPollAt = now.Add(time.Duration(v.PollIntervalSeconds) * time.Second)
		out = v
		return tx.SaveSource(v)
	})
	return
}

func (s *Service) CompletePoll(ctx context.Context, job Source, items []Content, notModified bool, etag, modified string, fetchErr error) (out IngestResult, err error) {
	out.Items = []Item{}
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Source(job.ID)
		if e != nil {
			return e
		}
		now := s.Now()
		if v.PollToken == "" || v.PollToken != job.PollToken || !v.Enabled || !v.PollUntil.After(now) {
			return ErrConflict
		}
		if fetchErr == nil && !notModified && len(items) > 0 {
			out, e = s.ingest(tx, v, items)
			if e != nil {
				return e
			}
		}
		v.PollToken = ""
		v.PollUntil = time.Time{}
		v.LastFetchedAt = &now
		if fetchErr != nil {
			v.LastError = "采集失败，请检查地址、网络或 Feed 格式"
		} else {
			v.LastError = ""
			v.LastSuccessAt = &now
			if len(etag) <= 512 {
				v.ETag = etag
			}
			if len(modified) <= 128 {
				v.LastModified = modified
			}
		}
		return tx.SaveSource(v)
	})
	return
}
