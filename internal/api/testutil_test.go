package api

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/blob"
	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/ratelimit"
	"github.com/anup-a/byagent/internal/store"
)

// newTestServer boots sqlite+local in a temp dir. managed=false uses ADMIN_TOKEN "admin-token".
func newTestServer(t *testing.T, managed bool) (*httptest.Server, Deps, string) {
	t.Helper()
	return newTestServerWith(t, managed, nil)
}

// newTestServerWith lets a test adjust the config and deps before the
// handler is built.
func newTestServerWith(t *testing.T, managed bool, adjust func(*Deps)) (*httptest.Server, Deps, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{DB: "sqlite", Storage: "local", DataDir: dir, ShareOrigin: "http://share.test", APIOrigin: "http://api.test",
		Limits: config.Limits{VersionBytes: 1 << 20, Files: 50, VersionsKept: 3, PublishPerMin: 100, ArtifactsPerTenant: 5, CommentBytes: 200, CommentsPerMin: 100, AnchorBytes: 1024}}
	if managed {
		cfg.KeySecret = "s"
	} else {
		cfg.AdminToken = "admin-token"
		cfg.KeySecret = "s"
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
		PublishLimiter: ratelimit.New(cfg.Limits.PublishPerMin), CommentLimiter: ratelimit.New(cfg.Limits.CommentsPerMin),
		PortalLimiter: ratelimit.New(60),
		Runtime:       []byte("/*runtime*/")}
	if adjust != nil {
		adjust(&d)
	}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { st.Close() })
	t.Cleanup(srv.Close)
	return srv, d, "admin-token"
}

// mintKey creates a tenant of the given kind with one key and returns the
// plaintext key. Keys are created in the app, never over the API.
func mintKey(t *testing.T, d Deps, kind string) string {
	t.Helper()
	now := d.Now()
	tenant := store.Tenant{ID: "t_" + ids.NewArtifactID(), Kind: kind, Name: kind + " tenant", CreatedAt: now}
	if err := d.Store.CreateTenant(context.Background(), tenant, ""); err != nil {
		t.Fatal(err)
	}
	return mintKeyIn(t, d, tenant.ID)
}

// mintKeyIn adds a key to an existing tenant and returns the plaintext key.
func mintKeyIn(t *testing.T, d Deps, tenantID string) string {
	t.Helper()
	plain, hash := ids.NewKey()
	if _, err := d.Store.CreateTenantKey(context.Background(), tenantID, hash, "k@x.io", "test", plain[:9], d.Now()); err != nil {
		t.Fatal(err)
	}
	return plain
}
