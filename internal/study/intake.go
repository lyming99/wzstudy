package study

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

type IntakeItem struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
}

type IntakeReceipt struct {
	ID          string       `json:"id"`
	SourceID    string       `json:"source_id"`
	TaskID      string       `json:"task_id,omitempty"`
	Integration string       `json:"integration"`
	Created     int          `json:"created"`
	Updated     int          `json:"updated"`
	Unchanged   int          `json:"unchanged"`
	Skipped     int          `json:"skipped"`
	Published   int          `json:"published"`
	Items       []IntakeItem `json:"items"`
	ReceivedAt  time.Time    `json:"received_at"`
}

type IntakePage struct {
	Items  []IntakeReceipt `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func (s *Service) recordIntake(tx Tx, source Source, result IngestResult, override ...string) (out IntakeReceipt, err error) {
	integration := source.Integration
	if source.System {
		integration = "manual"
	} else if source.Kind == "rss" {
		integration = "rss"
	} else if integration == "" {
		integration = "collector"
	}
	if len(override) > 0 {
		integration = override[0]
	}
	out = IntakeReceipt{ID: uuid.NewString(), SourceID: source.ID, Integration: integration, Created: result.Created, Updated: result.Updated, Unchanged: result.Unchanged, Published: result.Published, Items: []IntakeItem{}, ReceivedAt: s.Now()}
	if len(override) > 1 {
		out.TaskID = override[1]
	}
	for _, item := range result.Items {
		out.Items = append(out.Items, IntakeItem{ID: item.ID, Revision: item.Revision, Status: ReviewState(item)})
	}
	err = recordSave(tx, "intake", out.ID, out)
	return
}

func (s *Service) IntakeHistory(ctx context.Context, sourceID string, limit, offset int) (out IntakePage, err error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return out, ErrInvalid
	}
	out = IntakePage{Items: []IntakeReceipt{}, Limit: limit, Offset: offset}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[IntakeReceipt](tx, "intake")
		if e != nil {
			return e
		}
		filtered := []IntakeReceipt{}
		for _, v := range all {
			if sourceID == "" || v.SourceID == sourceID {
				filtered = append(filtered, v)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].ReceivedAt.Equal(filtered[j].ReceivedAt) {
				return filtered[i].ID > filtered[j].ID
			}
			return filtered[i].ReceivedAt.After(filtered[j].ReceivedAt)
		})
		out.Total = len(filtered)
		if offset < out.Total {
			out.Items = filtered[offset:min(offset+limit, out.Total)]
		}
		return nil
	})
	return
}
