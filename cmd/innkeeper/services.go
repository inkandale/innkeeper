package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/backup"
	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
)

// services are what the api and worker both use.
type services struct {
	pool     *pgxpool.Pool
	lib      *library.Library
	registry *plugins.Registry
	store    *blobstore.Store
	box      *secrets.Box
	backups  backup.Settings
}

// openServices reads the settings both services need, loads the built-in and
// installed plugins, and connects to and migrates the database.
func openServices(ctx context.Context, log *slog.Logger, maxConnsAtLeast int) (*services, error) {
	cfg := map[string]string{}
	for _, k := range []string{"INNKEEPER_BLOB_DIR", "INNKEEPER_PLUGINS_DIR"} {
		if cfg[k] = os.Getenv(k); cfg[k] == "" {
			return nil, fmt.Errorf("%s is not set", k)
		}
	}
	box, err := secrets.FromEnv()
	if err != nil {
		return nil, err
	}
	backups, err := backup.SettingsFromEnv()
	if err != nil {
		return nil, err
	}
	// Plugins run in their own folder, so a relative blob dir would point elsewhere for them.
	blobDir, err := filepath.Abs(cfg["INNKEEPER_BLOB_DIR"])
	if err != nil {
		return nil, err
	}
	pool, err := openDB(ctx, maxConnsAtLeast)
	if err != nil {
		return nil, err
	}
	store := &blobstore.Store{Name: "library", Root: blobDir}
	dirs := plugins.Dirs{Builtin: os.Getenv("INNKEEPER_BUILTIN_PLUGINS_DIR"), Installed: cfg["INNKEEPER_PLUGINS_DIR"]}
	registry, err := plugins.Open(ctx, pool, dirs, store, box, os.Getenv("INNKEEPER_DATABASE_URL"), log)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &services{pool: pool, lib: library.New(pool, store), registry: registry,
		store: store, box: box, backups: backups}, nil
}

func (s *services) close() { s.pool.Close() }

// ensureSingletons creates the instances singleton plugins need, and detects
// sources if a sites plugin got one. The api and worker both run it, so
// either one starting first is enough.
func (s *services) ensureSingletons(ctx context.Context, log *slog.Logger) error {
	created, err := s.registry.EnsureSingletons(ctx)
	if err != nil {
		return err
	}
	if len(created) > 0 {
		log.Info("created singleton plugin instances", "plugins", created)
	}
	importer.EnqueueDetectForSites(ctx, jobs.New(s.pool), s.registry, log, created...)
	return nil
}

// openDB connects to INNKEEPER_DATABASE_URL and migrates the schema. The pool's
// MaxConns is raised to at least maxConnsAtLeast; 0 keeps the URL's setting.
// JIT is off unless the URL sets jit.
func openDB(ctx context.Context, maxConnsAtLeast int) (*pgxpool.Pool, error) {
	url := os.Getenv("INNKEEPER_DATABASE_URL")
	if url == "" {
		return nil, errors.New("INNKEEPER_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns < int32(maxConnsAtLeast) {
		cfg.MaxConns = int32(maxConnsAtLeast)
	}
	// Compiling a search's plan can take longer than running it.
	if _, ok := cfg.ConnConfig.RuntimeParams["jit"]; !ok {
		cfg.ConnConfig.RuntimeParams["jit"] = "off"
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return pool, nil
}

// signalContext ends on SIGINT or SIGTERM. After the first signal, default
// handling returns, so a second one kills a stuck shutdown.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); stop() }()
	return ctx, stop
}

// stopIdlePlugins stops mgr's idle plugins every minute until ctx ends.
func stopIdlePlugins(ctx context.Context, mgr *pluginhost.Manager) {
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
}
