package feed

import (
	"context"
	"log/slog"
	"time"

	"wzstudy/internal/study"
)

type FetchSource interface {
	Fetch(context.Context, study.Source) (Result, error)
}
type Worker struct {
	Service *study.Service
	Fetcher FetchSource
	Logger  *slog.Logger
}

func (w *Worker) Once(ctx context.Context, id string, force bool) (study.IngestResult, error) {
	job, err := w.Service.ClaimPoll(ctx, id, force)
	if err != nil {
		return study.IngestResult{}, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	result, fetchErr := w.Fetcher.Fetch(fetchCtx, job)
	cancel()
	out, err := w.Service.CompletePoll(ctx, job, result.Items, result.NotModified, result.ETag, result.LastModified, fetchErr)
	if err != nil && err != study.ErrConflict {
		// Validation/storage errors roll back the content transaction. Release the
		// lease in a separate transaction, even if the HTTP caller disconnected.
		// An obsolete/committed lease cannot be overwritten: CompletePoll rechecks it.
		cleanupCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, _ = w.Service.CompletePoll(cleanupCtx, job, nil, false, "", "", err)
		done()
	}
	if err != nil {
		return out, err
	}
	if fetchErr != nil {
		return out, fetchErr
	}
	return out, nil
}
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		w.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *Worker) poll(ctx context.Context) {
	sources, err := w.Service.Sources(ctx)
	if err != nil {
		w.Logger.Error("source scan failed")
		return
	}
	for _, v := range sources {
		if ctx.Err() != nil {
			return
		}
		if !v.Enabled || v.OwnerID == "" || v.Kind != "rss" || v.NextPollAt.After(time.Now().UTC()) {
			continue
		}
		if _, err = w.Once(study.WithWorkspace(ctx, v.OwnerID), v.ID, false); err != nil && err != study.ErrBusy && err != study.ErrConflict {
			w.Logger.Warn("feed poll failed", "source_id", v.ID)
		}
	}
}
