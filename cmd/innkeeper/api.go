package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/inkandale/innkeeper/internal/api"
	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/web"
)

func apiServer() error {
	listen := os.Getenv("INNKEEPER_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signalContext()
	defer stop()
	svc, err := openServices(ctx, log, 0)
	if err != nil {
		return err
	}
	defer svc.close()
	if err := svc.ensureSingletons(ctx, log); err != nil {
		return err
	}
	mgr := pluginhost.NewManager(api.NoCore, 5*time.Minute, log)
	defer mgr.Close()
	go stopIdlePlugins(ctx, mgr)

	stopping := make(chan struct{})
	s := &api.Server{Lib: svc.lib, Queue: jobs.New(svc.pool), Registry: svc.registry, Auth: auth.New(svc.pool),
		Targets: targets.New(svc.pool, svc.lib, svc.registry, mgr, log), Plugins: mgr, Log: log,
		PublicURL: os.Getenv("INNKEEPER_PUBLIC_URL"), Stopping: stopping, Backups: svc.backups,
		UI: web.Handler(web.Dist())}
	if err := s.Bootstrap(ctx, os.Getenv("INNKEEPER_OWNER_PASSWORD_HASH")); err != nil {
		return err
	}
	toks, err := provisionedTokens(os.Getenv)
	if err != nil {
		return err
	}
	if err := s.Auth.SetProvisionedTokens(ctx, toks); err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("api listening", "addr", listen, "plugins", len(svc.registry.Manifests()))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	close(stopping)
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
