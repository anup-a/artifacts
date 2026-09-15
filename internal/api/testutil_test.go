package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
)

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

// newTestServer boots sqlite+local in a temp dir. managed=false uses ADMIN_TOKEN "admin-token".
func newTestServer(t *testing.T, managed bool) (*httptest.Server, Deps, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{DB: "sqlite", Storage: "local", DataDir: dir, ShareOrigin: "http://share.test", APIOrigin: "http://api.test",
		Limits: config.Limits{VersionBytes: 1 << 20, Files: 50, VersionsKept: 3, PublishPerMin: 100, ArtifactsPerKey: 5, CommentBytes: 200, CommentsPerMin: 100, AnchorBytes: 1024}}
	if managed {
		cfg.KeySecret = "s"
	} else {
		cfg.AdminToken = "admin-token"
	}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	bl, err := blob.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Cfg: cfg, Store: st, Blobs: bl, Now: func() time.Time { return time.Now().UTC() },
		PublishLimiter: ratelimit.New(cfg.Limits.PublishPerMin), CommentLimiter: ratelimit.New(cfg.Limits.CommentsPerMin), KeyLimiter: ratelimit.New(5),
		Runtime: []byte("/*runtime*/")}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { st.Close() })
	t.Cleanup(srv.Close)
	return srv, d, "admin-token"
}
