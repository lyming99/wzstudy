package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"wzstudy-agent/internal/config"
	"wzstudy-agent/internal/loop"
	"wzstudy-agent/internal/serverapi"
)

type Worker struct {
	Config config.Config
	Client *serverapi.Client
	Runner *loop.Runner
	Logger *slog.Logger
}

func (w *Worker) Run(ctx context.Context) error {
	status, err := w.Client.Status(ctx)
	if err != nil {
		return fmt.Errorf("服务器不可用或密钥无效: %w", err)
	}
	w.Logger.Info("agent started", "server", w.Config.ServerURL, "agent", status.Agent.Name, "lease_seconds", status.LeaseSeconds)
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.Once(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var apiErr *serverapi.Error
			if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
				return fmt.Errorf("采集端认证被拒绝: %w", err)
			}
			w.Logger.Warn("claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.Config.PollEvery):
		}
	}
}

func (w *Worker) Once(ctx context.Context) error {
	task, err := w.Client.Claim(ctx, 1, w.Config.WaitSeconds)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}
	w.execute(ctx, task)
	return nil
}

func (w *Worker) execute(parent context.Context, task *serverapi.Task) {
	taskCtx, cancel := context.WithCancel(parent)
	if task.Deadline != nil {
		cancel()
		taskCtx, cancel = context.WithDeadline(parent, *task.Deadline)
	}
	defer cancel()
	var remoteCancel atomic.Bool
	go w.heartbeat(taskCtx, task, cancel, &remoteCancel)

	emit := func(events []serverapi.EventInput) error {
		return w.Client.Events(taskCtx, task.ID, task.Attempts, events)
	}
	outcome := w.Runner.Run(taskCtx, task, emit)

	reportCtx, done := context.WithTimeout(context.WithoutCancel(parent), 15*time.Second)
	defer done()

	switch {
	case remoteCancel.Load():
		outcome.Status = serverapi.StatusCancelled
		outcome.Summary = "任务被管理员取消"
	case parent.Err() != nil:
		// Local shutdown or operator interrupt: return the lease instead of
		// recording a terminal failure so another attempt can run.
		if err := w.Client.Release(reportCtx, task.ID, task.Attempts, "采集端退出，归还任务"); err != nil {
			w.Logger.Warn("release failed", "task_id", task.ID, "error", err)
		}
		w.Logger.Info("task released", "task_id", task.ID)
		return
	case outcome.Status == serverapi.StatusCancelled:
		outcome.Status = serverapi.StatusFailed
		outcome.Error = &serverapi.TaskError{Code: "timeout", Message: "任务超时或上下文结束"}
	}
	if outcome.Status == "" {
		outcome.Status = serverapi.StatusFailed
		outcome.Error = &serverapi.TaskError{Code: "internal_error", Message: "采集端未给出结果"}
	}
	if err := w.Client.Complete(reportCtx, task.ID, task.Attempts, outcome.Status, outcome.Summary, outcome.Stats, outcome.Error); err != nil {
		w.Logger.Error("complete failed", "task_id", task.ID, "error", err)
		return
	}
	w.Logger.Info("task finished",
		"task_id", task.ID,
		"status", outcome.Status,
		"created", outcome.Stats.ItemsCreated,
		"updated", outcome.Stats.ItemsUpdated,
		"rounds", outcome.Stats.Rounds,
		"tool_calls", outcome.Stats.ToolCalls,
		"duration_ms", outcome.Stats.DurationMS,
	)
}

func (w *Worker) heartbeat(ctx context.Context, task *serverapi.Task, cancel context.CancelFunc, remote *atomic.Bool) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			heartbeat, err := w.Client.Heartbeat(ctx, task.ID)
			if err != nil {
				var apiErr *serverapi.Error
				if errors.As(err, &apiErr) && (apiErr.Status == 409 || apiErr.Status == 404 || apiErr.Status == 403) {
					// The lease is gone; stop writing immediately.
					remote.Store(true)
					cancel()
					return
				}
				w.Logger.Warn("heartbeat failed", "task_id", task.ID, "error", err)
				continue
			}
			if heartbeat.CancelRequested {
				remote.Store(true)
				cancel()
				return
			}
		}
	}
}
