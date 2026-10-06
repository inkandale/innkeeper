package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

const (
	blobGCKind   = "blob.gc"
	jobPruneKind = "job.prune"
	// jobRetention is how long finished jobs stay in the job list.
	jobRetention = 30 * 24 * time.Hour
)

func worker() error {
	concurrency := 4
	if v := os.Getenv("INNKEEPER_WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("INNKEEPER_WORKER_CONCURRENCY must be a positive number")
		}
		concurrency = n
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signalContext()
	defer stop()
	// Each running job can hold a connection (a push holds its transaction across
	// the plugin call) while its lease heartbeat or an import notification needs another.
	svc, err := openServices(ctx, log, 2*concurrency+2)
	if err != nil {
		return err
	}
	defer svc.close()

	im := importer.New(svc.lib, svc.registry, log)
	mgr := pluginhost.NewManager(im.Handle, 5*time.Minute, log)
	defer mgr.Close()
	im.Plugins = mgr
	go stopIdlePlugins(ctx, mgr)

	tg := targets.New(svc.pool, svc.lib, svc.registry, mgr, log)
	q := jobs.New(svc.pool)
	w := &jobs.Worker{
		Queue:       q,
		Log:         log,
		Concurrency: concurrency,
		Handlers: map[string]jobs.Handler{
			importer.Kind:         im.Run,
			bulk.ArchiveKind:      bulk.Archive(svc.lib),
			targets.PushKind:      tg.RunPush,
			targets.ScanKind:      tg.RunScan,
			targets.ImportKind:    tg.RunImport,
			targets.ReconcileKind: tg.RunReconcile,
			targets.ResolveKind:   tg.RunResolve,
			blobGCKind: func(ctx context.Context, r *jobs.Run) error {
				blobs, leftovers, err := svc.lib.CollectGarbage(ctx, 24*time.Hour)
				r.SetProgress(fmt.Appendf(nil, `{"blobs": %d, "leftovers": %d}`, blobs, leftovers))
				return err
			},
			jobPruneKind: func(ctx context.Context, r *jobs.Run) error {
				n, err := q.Prune(ctx, jobRetention)
				r.SetProgress(fmt.Appendf(nil, `{"deleted": %d}`, n))
				return err
			},
		},
		Schedules: []jobs.Schedule{
			{Kind: blobGCKind, Every: 24 * time.Hour},
			{Kind: jobPruneKind, Every: 24 * time.Hour},
			{Kind: targets.ScanKind, Every: time.Hour},
		},
	}
	log.Info("worker started", "plugins", len(svc.manifests), "concurrency", concurrency)
	return w.Run(ctx)
}
