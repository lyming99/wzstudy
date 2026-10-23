package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"wzstudy/internal/study"
	"wzstudy/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store, err := study.OpenPostgres(ctx, dsn)
	if err != nil {
		return fmt.Errorf("database connection failed")
	}
	defer store.Close()
	if err = migrations.Apply(ctx, store.Pool); err != nil {
		return fmt.Errorf("database migration failed: %w", err)
	}
	fmt.Println("database migrations applied")
	return nil
}
