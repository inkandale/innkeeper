package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/davidgibbons/innkeeper/internal/backup"
	"github.com/davidgibbons/innkeeper/internal/bulk"
	exporter "github.com/davidgibbons/innkeeper/internal/export"
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
	ex := &exporter.Exporter{Lib: svc.lib, Registry: svc.registry, Plugins: mgr, Log: log}
	q := jobs.New(svc.pool)
	dbURL := os.Getenv("INNKEEPER_DATABASE_URL")
	backupOpts := backup.Options{DatabaseURL: dbURL, Store: svc.store, Box: svc.box, Log: log}
	restoreOpts := backup.RestoreOptions{Conn: svc.pool.Config().ConnConfig, Store: svc.store, Box: svc.box, Log: log}
	// A restored config brings back plugin instances whose databases must exist.
	afterConfig := func(ctx context.Context) error { return svc.registry.ProvisionDatabases(ctx, dbURL) }
	w := &jobs.Worker{
		Queue:       q,
		Log:         log,
		Concurrency: concurrency,
		Handlers: map[string]jobs.Handler{
			importer.Kind:              im.Run,
			bulk.ArchiveKind:           bulk.Archive(svc.lib),
			bulk.LorebookArchiveKind:   bulk.ArchiveLorebook(svc.lib),
			bulk.TagKind:               bulk.Tag(svc.lib),
			bulk.CollectKind:           bulk.Collect(svc.lib),
			bulk.ExportKind:            bulk.Export(ex),
			targets.PushKind:           tg.RunPush,
			targets.ScanKind:           tg.RunScan,
			targets.ImportKind:         tg.RunImport,
			targets.ReconcileKind:      tg.RunReconcile,
			targets.ResolveKind:        tg.RunResolve,
			backup.Kind(backup.Assets): backup.BackupHandler(svc.backups, svc.pool, backupOpts, backup.Assets),
			backup.Kind(backup.Config): backup.BackupHandler(svc.backups, svc.pool, backupOpts, backup.Config),
			backup.RestoreKind:         backup.RestoreHandler(svc.backups, restoreOpts, afterConfig),
			blobGCKind: func(ctx context.Context, r *jobs.Run) error {
				blobs, leftovers, err := svc.lib.CollectGarbage(ctx, 24*time.Hour)
				if err == nil && svc.backups.Dir != "" {
					var uploads int
					uploads, err = backup.SweepUploads(ctx, svc.pool, svc.backups.Dir, time.Hour)
					leftovers += uploads
				}
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
	// Plugins installed or removed through the API take effect at the next job.
	for kind, h := range w.Handlers {
		w.Handlers[kind] = func(ctx context.Context, r *jobs.Run) error {
			if err := svc.registry.Sync(ctx); err != nil {
				return err
			}
			return h(ctx, r)
		}
	}
	for t, every := range svc.backups.Every {
		w.Schedules = append(w.Schedules, jobs.Schedule{Kind: backup.Kind(t), Every: every})
	}
	log.Info("worker started", "plugins", len(svc.registry.Manifests()), "concurrency", concurrency)
	return w.Run(ctx)
}
