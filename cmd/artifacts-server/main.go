package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/anup-a/artifacts/internal/domains"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/snapshot"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/internal/usage"
	"github.com/anup-a/artifacts/runtime"
)

const (
	viewFlushEvery    = 30 * time.Second
	shutdownTimeout   = 10 * time.Second
	portalLinksPerMin = 60
	domainCheckEvery  = time.Minute
)

// dotenvKeys are the settings a server move must carry over. Anything that
// only describes the old host (ADDR, DATA_DIR, Fly's own vars) is left out.
var dotenvKeys = []string{
	"ADMIN_TOKEN", "KEY_SIGNING_SECRET",
	"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET",
	"EMAIL_PROVIDER", "RESEND_API_KEY", "EMAIL_FROM",
	"GOOGLE_ANALYTICS_ID", "GA_MEASUREMENT_ID", "GUEST_PUBLISH",
	"PUBLIC_SHARE_ORIGIN", "PUBLIC_API_ORIGIN", "CONTENT_DOMAIN",
	"LIMIT_VERSION_BYTES", "LIMIT_FILES", "LIMIT_VERSIONS_KEPT", "LIMIT_PUBLISH_PER_MIN",
	"LIMIT_ARTIFACTS_PER_TENANT", "LIMIT_ARTIFACTS_PER_KEY",
	"LIMIT_COMMENT_BYTES", "LIMIT_COMMENTS_PER_MIN", "LIMIT_ANCHOR_BYTES",
	"LIMIT_ACCESS_MAIL_PER_WORKSPACE_DAY", "LIMIT_ACCESS_MAIL_PER_DAY",
	"VARIABLES_KEY", "VARIABLES", "LIMIT_PROXY_PER_IP_PER_HOUR", "LIMIT_PROXY_PER_PAGE_PER_HOUR",
	// The curated /showcase.
	"SHOWCASE_PAGES", "SHOWCASE_COLLECTION",
	// Custom domains. DOMAINS_PROVIDER and FLY_* are left out on purpose:
	// they describe the old host's edge, and the new one sets its own.
	"DOMAINS_CNAME_TARGET", "DOMAINS_APEX_IPS", "DOMAINS_DNS_RESOLVER",
	"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID", "LIMIT_DOMAINS_PER_TENANT",
}

func main() {
	// `artifacts-server dotenv` prints the server's own settings as .env lines,
	// for a server move. The image has no shell or printenv, and only someone
	// who can already exec into the machine can run it.
	if len(os.Args) == 2 && os.Args[1] == "dotenv" {
		for _, k := range dotenvKeys {
			if v := os.Getenv(k); v != "" {
				fmt.Printf("%s=%s\n", k, v)
			}
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// `artifacts-server snapshot <file.tgz>` packs DATA_DIR for a server move
	// and exits. The image has no shell, so this is how it is driven:
	// fly ssh console -C "/artifacts-server snapshot /data/snapshot.tgz".
	if len(os.Args) == 3 && os.Args[1] == "snapshot" {
		if err := writeSnapshot(ctx, cfg, os.Args[2]); err != nil {
			log.Error("snapshot", "err", err)
			os.Exit(1)
		}
		log.Info("snapshot written", "path", os.Args[2])
		return
	}
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
	views.TrustProxy, views.Now = cfg.TrustProxyHeaders, now
	webApp := app.New(st, cfg, now)
	// Nil when DOMAINS_PROVIDER is unset, which turns custom domains off.
	doms := domains.New(cfg, st, now)
	webApp.Domains = doms
	deps := api.Deps{
		Cfg: cfg, Store: st, Blobs: bl, Now: now,
		PublishLimiter: ratelimit.New(cfg.Limits.PublishPerMin),
		CommentLimiter: ratelimit.New(cfg.Limits.CommentsPerMin),
		PortalLimiter:  ratelimit.New(portalLinksPerMin),
		Runtime:        runtime.CommentsJS,
		OnView:         views.Record,
		Web:            webApp.Register,
		WebDeny:        webApp.DenyArtifactAccess,
		WebGate:        webApp.GateArtifactAccess,
		WebGuide:       webApp.GuidePage,
		Domains:        doms,
	}
	h, err := api.NewHandler(deps)
	if err != nil {
		log.Error("handler", "err", err)
		os.Exit(1)
	}
	go api.RunGC(ctx, deps, time.Hour)
	if doms != nil {
		go doms.Run(ctx, domainCheckEvery, func() bool { return cfg.ReadOnly })
	}
	flushed := make(chan struct{})
	go func() {
		views.Run(ctx, viewFlushEvery)
		close(flushed)
	}()
	log.Info("listening", "addr", cfg.Addr, "db", cfg.DB, "storage", cfg.Storage, "web", cfg.WebEnabled())
	// Headers must arrive within 10 s and an idle keep-alive connection is
	// closed after 2 min. There is no whole-request ReadTimeout: a publish
	// upload on a slow link can rightly take minutes. Handlers that must not
	// wait on a slow body set their own deadline (proxy routes: 10 s).
	srv := &http.Server{Addr: cfg.Addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
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

// writeSnapshot writes the archive beside its destination and renames it into
// place, so a half-written file is never mistaken for a finished one.
func writeSnapshot(ctx context.Context, cfg config.Config, dst string) error {
	if cfg.DB != "sqlite" || cfg.Storage != "local" {
		return errors.New("snapshot covers DB=sqlite with STORAGE=local only")
	}
	tmp := dst + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := snapshot.Write(ctx, cfg.DataDir, f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
