package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/anup-a/artifacts/internal/api"
	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/runtime"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx := context.Background()
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
	h, err := api.NewHandler(api.Deps{
		Cfg: cfg, Store: st, Blobs: bl, Now: func() time.Time { return time.Now().UTC() },
		PublishLimiter: ratelimit.New(cfg.Limits.PublishPerMin),
		CommentLimiter: ratelimit.New(cfg.Limits.CommentsPerMin),
		KeyLimiter:     ratelimit.New(5),
		Runtime:        runtime.CommentsJS,
	})
	if err != nil {
		log.Error("handler", "err", err)
		os.Exit(1)
	}
	log.Info("listening", "addr", cfg.Addr, "db", cfg.DB, "storage", cfg.Storage)
	srv := &http.Server{Addr: cfg.Addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}
