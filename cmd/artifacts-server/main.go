package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anup-a/artifacts/internal/api"
	"github.com/anup-a/artifacts/internal/app"
	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/internal/usage"
	"github.com/anup-a/artifacts/runtime"
)

const (
	viewFlushEvery    = 30 * time.Second
	shutdownTimeout   = 10 * time.Second
	portalLinksPerMin = 60
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		log.Error("store", "err", err)
		os.Exit(1)
	}
	if err := st.Migrate(ctx); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	bl, err := blob.Open(cfg)
	if err != nil {
		log.Error("blob", "err", err)
		os.Exit(1)
	}
	now := func() time.Time { return time.Now().UTC() }
	views := usage.New(st)
	webApp := app.New(st, cfg, now)
	deps := api.Deps{
		Cfg: cfg, Store: st, Blobs: bl, Now: now,
		PublishLimiter: ratelimit.New(cfg.Limits.PublishPerMin),
		CommentLimiter: ratelimit.New(cfg.Limits.CommentsPerMin),
		PortalLimiter:  ratelimit.New(portalLinksPerMin),
		Runtime:        runtime.CommentsJS,
		OnView:         func(id string) { views.Hit(id, now()) },
		Web:            webApp.Register,
		WebDeny:        webApp.DenyArtifactAccess,
	}
	h, err := api.NewHandler(deps)
	if err != nil {
		log.Error("handler", "err", err)
		os.Exit(1)
	}
	go api.RunGC(ctx, deps, time.Hour)
	flushed := make(chan struct{})
	go func() {
		views.Run(ctx, viewFlushEvery)
		close(flushed)
	}()
	log.Info("listening", "addr", cfg.Addr, "db", cfg.DB, "storage", cfg.Storage, "web", cfg.WebEnabled())
	srv := &http.Server{Addr: cfg.Addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
	stop()
	<-flushed
	if err := st.Close(); err != nil {
		log.Error("close", "err", err)
	}
	log.Info("stopped")
}
