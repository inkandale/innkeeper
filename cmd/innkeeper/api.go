package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/davidgibbons/innkeeper/internal/api"
	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
)

func apiServer() error {
	listen := os.Getenv("INNKEEPER_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signalContext()
	defer stop()
	svc, err := openServices(ctx, log)
	if err != nil {
		return err
	}
	defer svc.close()
	mgr := pluginhost.NewManager(api.NoCore, 5*time.Minute, log)
	defer mgr.Close()
	go stopIdlePlugins(ctx, mgr)

	s := &api.Server{Lib: svc.lib, Queue: jobs.New(svc.pool), Registry: svc.registry, Auth: auth.New(svc.pool),
		Plugins: mgr, Log: log, PublicURL: os.Getenv("INNKEEPER_PUBLIC_URL")}
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Requests end with ctx, so open job streams don't hold up shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("api listening", "addr", listen, "plugins", len(svc.manifests))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
