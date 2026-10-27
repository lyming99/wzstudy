package study

import "context"

// PublishPendingIngested makes material that an earlier ingest path queued for
// review public, because a successful push now publishes immediately and the
// client query API must expose the same material after an upgrade.
//
// Only items pushed through a dedicated ingest source (Skill, MCP, collector or
// RSS) are candidates. Items created inside the admin workspace keep their review
// state: drafts stay drafts and pending manual uploads still need approval.
// It is idempotent: published, unpublished and deleted items are skipped, so a
// restart never emits duplicate change events.
func (s *Service) PublishPendingIngested(ctx context.Context) (published int, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		items, e := allItems(tx)
		if e != nil {
			return e
		}
		now := s.Now()
		for _, v := range items {
			if v.DeletedAt != nil || ReviewState(v) != "pending" {
				continue
			}
			source, e := tx.Source(v.SourceID)
			if e != nil {
				return e
			}
			if source.System || !pushSource(source) {
				continue
			}
			v.Revision++
			v.UpdatedAt = now
			if e = s.publishWithLabel(tx, &v, true, "历史推送内容已发布"); e != nil {
				return e
			}
			if e = tx.SaveItem(v); e != nil {
				return e
			}
			published++
		}
		return nil
	})
	return published, err
}

// pushSource reports whether a source can only have received material through
// the push/ingest path, never through interactive admin editing. Sources created
// before the integration marker existed have an empty Integration value, so they
// must keep being recognized as push sources after an upgrade. The workspace's
// own source is recognized by Source.System and is never a candidate.
func pushSource(v Source) bool {
	if v.Kind == "rss" {
		return true
	}
	if v.Kind != "manual" {
		return false
	}
	switch v.Integration {
	case "", "collector", "skill", "mcp":
		return true
	}
	return false
}
