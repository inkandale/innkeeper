package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/inkandale/innkeeper/internal/actions"
	"github.com/inkandale/innkeeper/internal/api"
	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/backup"
	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/enrich"
	exporter "github.com/inkandale/innkeeper/internal/export"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/maintenance"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	iservices "github.com/inkandale/innkeeper/internal/services"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/protocol"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	syncEvery, err := everyEnv("INNKEEPER_SYNC_EVERY", time.Hour)
	if err != nil {
		return err
	}
	sourceCheckEvery, err := everyEnv("INNKEEPER_SOURCE_CHECK_EVERY", 24*time.Hour)
	if err != nil {
		return err
	}
	// How long finished jobs, and so their logs, stay in the job list.
	jobRetention, err := everyEnv("INNKEEPER_JOB_RETENTION", 7*24*time.Hour)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signalContext()
	defer stop()
	var metrics *jobs.Metrics
	if addr := os.Getenv("INNKEEPER_METRICS_LISTEN"); addr != "" {
		if metrics, err = serveMetrics(ctx, addr, log); err != nil {
			return err
		}
	}
	// Each running job can hold a connection (a push holds its transaction across
	// the plugin call) while its lease heartbeat or an import notification needs another.
	svc, err := openServices(ctx, log, 2*concurrency+2)
	if err != nil {
		return err
	}
	defer svc.close()
	if err := svc.ensureSingletons(ctx, log); err != nil {
		return err
	}

	q := jobs.New(svc.pool)
	runs := pluginhost.NewRuns(log)
	broker := &iservices.Broker{Registry: svc.registry, Runs: runs}
	mgr := pluginhost.NewManager(broker.Handle(runs.Handle), 5*time.Minute, log)
	defer mgr.Close()
	broker.Plugins = mgr
	go stopIdlePlugins(ctx, mgr)

	tg := targets.New(svc.pool, svc.lib, svc.registry, mgr, log)
	im := importer.New(svc.lib, svc.registry, q, mgr, runs, tg, log)
	// Serves library.call's tools in process; nothing listens on it.
	tools := &api.Server{Lib: svc.lib, Queue: q, Registry: svc.registry, Auth: auth.New(svc.pool), Targets: tg, Plugins: mgr, Log: log}
	runner := &actions.Runner{Lib: svc.lib, Registry: svc.registry, Plugins: mgr, Runs: runs, API: tools.APIHandler()}
	enricher := &enrich.Runner{Lib: svc.lib, Registry: svc.registry, Plugins: mgr, Runs: runs}
	sweeper := &enrich.Sweeper{Lib: svc.lib, Registry: svc.registry, Queue: q}
	ex := &exporter.Exporter{Lib: svc.lib, Registry: svc.registry, Plugins: mgr, Log: log}
	maint := &maintenance.Runner{M: maintenance.New(svc.pool, svc.lib), Space: func(ctx context.Context) (int64, string, error) {
		inst, err := svc.registry.VectorInstance(ctx)
		if err != nil || inst == 0 {
			return 0, "", err
		}
		// The space vector.query answers in is the one current vectors use.
		v, err := svc.registry.VectorQuery(ctx, mgr, inst, "space probe")
		return inst, v.Space, err
	}}
	dbURL := os.Getenv("INNKEEPER_DATABASE_URL")
	backupOpts := backup.Options{DatabaseURL: dbURL, Store: svc.store, Box: svc.box, Log: log}
	restoreOpts := backup.RestoreOptions{Conn: svc.pool.Config().ConnConfig, Store: svc.store, Box: svc.box, Log: log}
	// A restored config brings back plugin instances whose databases must exist.
	afterConfig := func(ctx context.Context) error { return svc.registry.ProvisionDatabases(ctx, dbURL) }
	w := &jobs.Worker{
		Queue:       q,
		Log:         log,
		Concurrency: concurrency,
		Metrics:     metrics,
		Handlers: map[string]jobs.Handler{
			importer.Kind:            im.Run,
			importer.DetectKind:      im.RunDetect,
			actions.Kind:             runner.Run,
			enrich.Kind:              enricher.Run,
			enrich.SweepKind:         sweeper.Run,
			bulk.ArchiveKind:         bulk.Archive(svc.lib, tg),
			bulk.LorebookArchiveKind: bulk.ArchiveLorebook(svc.lib, tg),
			bulk.TagKind:             bulk.Tag(svc.lib),
			bulk.CollectKind:         bulk.Collect(svc.lib),
			bulk.ExportKind:          bulk.Export(ex),
			bulk.DeleteLibraryKind:   bulk.DeleteLibrary(svc.lib),
			bulk.PurgeKind:           bulk.Purge(svc.lib),
			maintenance.Kind:         maint.Run,
			bulk.HideKind: bulk.Hide(svc.lib, func(ctx context.Context, id int64, p protocol.AIDecideParams) (protocol.AIDecideResult, error) {
				return svc.registry.Decide(ctx, mgr, id, p)
			}),
			targets.PushKind:           tg.RunPush,
			targets.ScanKind:           tg.RunScan,
			targets.ReconcileKind:      tg.RunReconcile,
			targets.ResolveKind:        tg.RunResolve,
			targets.SyncKind:           tg.RunSync,
			targets.SourceCheckKind:    tg.RunSourceCheck,
			backup.Kind(backup.Assets): backup.BackupHandler(svc.backups, svc.pool, backupOpts, backup.Assets),
			backup.Kind(backup.Config): backup.BackupHandler(svc.backups, svc.pool, backupOpts, backup.Config),
			backup.RestoreKind:         backup.RestoreHandler(svc.backups, restoreOpts, afterConfig),
			library.GarbageKind: func(ctx context.Context, r *jobs.Run) error {
				blobs, leftovers, err := svc.lib.CollectGarbage(ctx, 24*time.Hour)
				if err == nil && svc.backups.Dir != "" {
					var uploads int
					uploads, err = backup.SweepUploads(ctx, svc.pool, svc.backups.Dir, time.Hour)
					leftovers += uploads
				}
				r.SetProgress(fmt.Appendf(nil, `{"blobs": %d, "leftovers": %d}`, blobs, leftovers))
				return err
			},
			jobs.PruneKind: func(ctx context.Context, r *jobs.Run) error {
				n, err := q.Prune(ctx, jobRetention)
				r.SetProgress(fmt.Appendf(nil, `{"deleted": %d}`, n))
				return err
			},
		},
		Schedules: []jobs.Schedule{
			{Kind: library.GarbageKind, Every: 24 * time.Hour},
			{Kind: targets.ScanKind, Every: time.Hour},
			{Kind: enrich.SweepKind, Every: time.Hour},
			// ponytail: catches entities queued while a detect job was finishing, and enqueues that failed; hourly is the delay then.
			{Kind: importer.DetectKind, Every: time.Hour},
		},
	}
	if syncEvery > 0 {
		w.Schedules = append(w.Schedules, jobs.Schedule{Kind: targets.SyncKind, Every: syncEvery})
	}
	if jobRetention > 0 {
		w.Schedules = append(w.Schedules, jobs.Schedule{Kind: jobs.PruneKind, Every: 24 * time.Hour})
	}
	if sourceCheckEvery > 0 {
		w.Schedules = append(w.Schedules, jobs.Schedule{Kind: targets.SourceCheckKind, Every: sourceCheckEvery})
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

// serveMetrics serves the job metrics, with Go runtime and process metrics,
// at /metrics on addr until ctx is cancelled.
func serveMetrics(ctx context.Context, addr string, log *slog.Logger) (*jobs.Metrics, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := jobs.NewMetrics(reg)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("INNKEEPER_METRICS_LISTEN: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server stopped", "err", err)
		}
	}()
	context.AfterFunc(ctx, func() { srv.Close() })
	log.Info("metrics listening", "addr", ln.Addr().String())
	return metrics, nil
}

// everyEnv reads a schedule from env var name: a duration such as 1h, or 0
// for none. def is the schedule when it's unset.
func everyEnv(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s: %q is not a duration like 1h, or 0 for no schedule", name, v)
	}
	return d, nil
}
