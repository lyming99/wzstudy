package study

import (
	"context"
)

// Pausing must still work if the bound source or agent has since been removed.
func (s *Service) SetCollectionPlanEnabled(ctx context.Context, id string, revision int64, enabled bool) (out CollectionPlan, err error) {
	if WorkspaceUserID(ctx) == "" {
		return out, ErrUnauthorized
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		rec, e := recordAs[collectionPlanRecord](tx, collectionPlanKind, id)
		if e != nil {
			return e
		}
		if rec.DeletedAt != nil {
			return ErrNotFound
		}
		if rec.Revision != revision {
			return ErrConflict
		}
		if rec.Enabled == enabled {
			out = rec.CollectionPlan
			return nil
		}
		now := s.Now().UTC()
		if enabled {
			if e := validateCollectionTaskBinding(tx, rec.Task); e != nil {
				return e
			}
			if rec.Schedule.Kind == "once" && !rec.Schedule.At.After(now) {
				return ErrInvalid
			}
			rec.NextRunAt = nextCollectionTime(rec.Schedule, now)
		} else {
			rec.NextRunAt = nil
		}
		rec.Enabled = enabled
		rec.Revision++
		rec.UpdatedAt = now
		out = rec.CollectionPlan
		return recordSave(tx, collectionPlanKind, id, rec)
	})
	return
}
