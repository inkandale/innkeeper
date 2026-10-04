// Command innkeeper is the Innkeeper server and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/contract"
	"github.com/davidgibbons/innkeeper/internal/db"
)

const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate    (reads INNKEEPER_DATABASE_URL)
  innkeeper worker     (reads INNKEEPER_DATABASE_URL, INNKEEPER_BLOB_DIR,
                        INNKEEPER_PLUGINS_DIR, INNKEEPER_WORKER_CONCURRENCY)`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "innkeeper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 3 && args[0] == "plugin" && args[1] == "check" {
		return pluginCheck(args[2])
	}
	if len(args) == 1 && args[0] == "migrate" {
		return migrate()
	}
	if len(args) == 1 && args[0] == "worker" {
		return worker()
	}
	return errors.New(usage)
}

func pluginCheck(dir string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	results, err := contract.Check(context.Background(), dir, log)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
		} else {
			fmt.Printf("PASS  %s\n", r.Name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	}
	return nil
}

func migrate() error {
	url := os.Getenv("INNKEEPER_DATABASE_URL")
	if url == "" {
		return errors.New("INNKEEPER_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool)
}
