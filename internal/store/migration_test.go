package store

import (
	"context"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/config"
)

func TestV1TenantBackfill(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, config.Config{DB: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := st.(*sqlStore)
	initial, err := migrationFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, string(initial)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, email := range []string{"admin@local", "one@example.test", "two@example.test"} {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO keys(id,key_sha256,email,created_at) VALUES (?,?,?,?)`, i+1, email, email, now); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.ExecContext(ctx, `INSERT INTO artifacts(id,key_id,title,current_version,created_at,updated_at) VALUES (?,?,?,7,?,?)`, email, i+1, "Original", now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, email := range []string{"admin@local", "one@example.test", "two@example.test"} {
		k, err := s.KeyByHash(ctx, email)
		if err != nil {
			t.Fatal(err)
		}
		if k.TenantID == "" || seen[k.TenantID] {
			t.Fatal("tenants must be distinct")
		}
		seen[k.TenantID] = true
		a, err := s.Artifact(ctx, email)
		if err != nil || a.TenantID != k.TenantID || a.ID != email || a.CurrentVersion != 7 {
			t.Fatalf("backfill: %v %+v", err, a)
		}
		if !a.CommentsEnabled {
			t.Fatalf("pre-existing artifacts must keep comments on: %+v", a)
		}
		tenant, err := s.Tenant(ctx, k.TenantID)
		if err != nil || tenant.Kind != "personal" || tenant.Name != email {
			t.Fatalf("tenant: %v %+v", err, tenant)
		}
		if email == "admin@local" && k.TenantID != "admin" {
			t.Fatal("admin tenant")
		}
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	var violations int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign keys: %v %d", err, violations)
	}
}
