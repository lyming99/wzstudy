package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"wzstudy/internal/config"
	"wzstudy/internal/feed"
	"wzstudy/internal/httpapi"
	"wzstudy/internal/study"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		r, e := client.Get(os.Args[2])
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var store study.Store
	if cfg.Store == "memory" {
		store = study.NewMemoryStore()
		logger.Warn("volatile memory mode: all data will be lost on restart")
	} else if cfg.Store == "file" {
		store, err = study.OpenFileStore(filepath.Join(cfg.DataDir, "workspace.json"))
		if err != nil {
			return fmt.Errorf("file store could not be opened; check DATA_DIR and file integrity")
		}
	} else {
		openCtx, done := context.WithTimeout(ctx, 10*time.Second)
		store, err = study.OpenPostgres(openCtx, cfg.DatabaseURL)
		done()
		if err != nil {
			return fmt.Errorf("database connection failed; verify DATABASE_URL and PostgreSQL availability")
		}
	}
	defer store.Close()
	readyCtx, done := context.WithTimeout(ctx, 5*time.Second)
	err = store.Ping(readyCtx)
	done()
	if err != nil {
		return fmt.Errorf("database schema is not ready; run cmd/migrate first")
	}
	service := study.NewService(store)
	bootstrapCtx, bootstrapDone := context.WithTimeout(ctx, 30*time.Second)
	_, created, bootstrapErr := service.EnsureAdministrator(bootstrapCtx, study.AdministratorSettings{Email: cfg.SystemAdminEmail, Password: cfg.SystemAdminPassword, DisplayName: cfg.SystemAdminDisplayName})
	bootstrapDone()
	if bootstrapErr != nil {
		return bootstrapErr
	}
	cfg.SystemAdminPassword = ""
	_ = os.Unsetenv("SYSTEM_ADMIN_PASSWORD")
	_ = os.Unsetenv("BOOTSTRAP_ADMIN_PASSWORD")
	logger.Info("system administrator ready", "created", created)
	worker := &feed.Worker{Service: service, Fetcher: feed.NewFetcher(), Logger: logger}
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if e := service.DispatchDue(ctx); e != nil && ctx.Err() == nil {
				logger.Error("push scheduler failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	workerDone := make(chan struct{})
	if cfg.PollEnabled {
		go func() { defer close(workerDone); worker.Run(ctx) }()
	} else {
		close(workerDone)
	}
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.New(cfg, service, worker, logger), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	errors := make(chan error, 1)
	go func() {
		logger.Info("wzstudy listening", "address", cfg.HTTPAddr, "store", cfg.Store)
		errors <- server.ListenAndServe()
	}()
	select {
	case err = <-errors:
		cancel()
		if err != http.ErrServerClosed {
			logger.Error("http listener failed")
			err = fmt.Errorf("HTTP listener failed; check HTTP_ADDR")
		} else {
			err = nil
		}
	case <-ctx.Done():
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if e := server.Shutdown(shutdownCtx); e != nil {
		_ = server.Close()
		return fmt.Errorf("HTTP shutdown timed out")
	}
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		return fmt.Errorf("worker shutdown timed out")
	}
	select {
	case <-schedulerDone:
	case <-shutdownCtx.Done():
		return fmt.Errorf("scheduler shutdown timed out")
	}
	return err
}
