package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

const blobGCKind = "blob.gc"

func worker() error {
	cfg := map[string]string{}
	for _, k := range []string{"INNKEEPER_DATABASE_URL", "INNKEEPER_BLOB_DIR", "INNKEEPER_PLUGINS_DIR"} {
		if cfg[k] = os.Getenv(k); cfg[k] == "" {
			return fmt.Errorf("%s is not set", k)
		}
	}
	concurrency := 4
	if v := os.Getenv("INNKEEPER_WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("INNKEEPER_WORKER_CONCURRENCY must be a positive number")
		}
		concurrency = n
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg["INNKEEPER_DATABASE_URL"])
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	store := &blobstore.Store{Name: "library", Root: cfg["INNKEEPER_BLOB_DIR"]}
	lib := library.New(pool, store)
	manifests, err := plugins.LoadDir(cfg["INNKEEPER_PLUGINS_DIR"], log)
	if err != nil {
		return err
	}
	im := importer.New(lib, plugins.New(pool, manifests, store), log)
	mgr := pluginhost.NewManager(im.Handle, 5*time.Minute, log)
	defer mgr.Close()
	im.Plugins = mgr
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mgr.StopIdle()
			}
		}
	}()

	w := &jobs.Worker{
		Queue:       jobs.New(pool),
		Log:         log,
		Concurrency: concurrency,
		Handlers: map[string]jobs.Handler{
			importer.Kind:    im.Run,
			bulk.ArchiveKind: bulk.Archive(lib),
			blobGCKind: func(ctx context.Context, r *jobs.Run) error {
				blobs, leftovers, err := lib.CollectGarbage(ctx, 24*time.Hour)
				r.SetProgress(fmt.Appendf(nil, `{"blobs": %d, "leftovers": %d}`, blobs, leftovers))
				return err
			},
		},
		Schedules: []jobs.Schedule{{Kind: blobGCKind, Every: 24 * time.Hour}},
	}
	log.Info("worker started", "plugins", len(manifests), "concurrency", concurrency)
	return w.Run(ctx)
}
