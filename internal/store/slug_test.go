package store

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ids"
)

var slugShape = regexp.MustCompile(`^[a-z2-7]{16}$`)

// emptyStore opens a store with no tables at all, so a test can migrate it
// part of the way. With TEST_PG_URL set it is a fresh Postgres schema of
// its own (dropped afterwards), so the shared test database is untouched.
func emptyStore(t *testing.T) *sqlStore {
	t.Helper()
	ctx := context.Background()
	url := os.Getenv("TEST_PG_URL")
	if url == "" {
		st, err := Open(ctx, config.Config{DB: "sqlite", DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st.(*sqlStore)
	}
	schema := "slugs_" + ids.NewSlug()
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	st, err := Open(ctx, config.Config{DB: "postgres", DatabaseURL: url + sep + "search_path=" + schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st.(*sqlStore)
}

// Pages published before 0019 have no slug. The migration gives every one
// of them, deleted ones included, a slug of its own in the same transaction,
// and leaves them alone when it runs again.
func TestSlugBackfill(t *testing.T) {
	ctx := context.Background()
	s := emptyStore(t)
	if err := migrateTo(ctx, s.db, s.q, slugMigration-1); err != nil {
		t.Fatal(err)
	}
	k, err := s.CreateKey(ctx, "hash-backfill", "old@example.test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	old := []string{"oldpage00001", "oldpage00002", "oldgone00003"}
	for _, id := range old {
		if _, err := s.db.ExecContext(ctx, s.q(`INSERT INTO artifacts(id,key_id,tenant_id,title,current_version,created_at,updated_at) VALUES (?,?,?,?,1,?,?)`),
			id, k.ID, k.TenantID, "Old "+id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET deleted_at=? WHERE id=?`), now, "oldgone00003"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	slugs := map[string]string{}
	seen := map[string]bool{}
	for _, id := range old {
		a, err := s.Artifact(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !slugShape.MatchString(a.Slug) || seen[a.Slug] {
			t.Fatalf("%s: slug %q", id, a.Slug)
		}
		seen[a.Slug] = true
		slugs[id] = a.Slug
		got, err := s.ArtifactBySlug(ctx, a.Slug)
		if err != nil || got.ID != id {
			t.Fatalf("by slug %q: %v %+v", a.Slug, err, got)
		}
	}
	if gone, _ := s.ArtifactBySlug(ctx, slugs["oldgone00003"]); gone.DeletedAt == nil {
		t.Fatal("a deleted page must still resolve by slug, as deleted, so its host answers 410")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal("migrate again:", err)
	}
	for _, id := range old {
		if a, _ := s.Artifact(ctx, id); a.Slug != slugs[id] {
			t.Fatalf("%s: slug changed on a second migrate: %q -> %q", id, slugs[id], a.Slug)
		}
	}
	// The backfill on its own is idempotent too: it only fills NULLs.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := backfillSlugs(ctx, tx, s.q); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Artifact(ctx, old[0]); a.Slug != slugs[old[0]] {
		t.Fatal("backfill rewrote a slug")
	}
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET slug=? WHERE id=?`), slugs[old[0]], old[1]); err == nil {
		t.Fatal("two pages may not share a slug")
	}
}

func TestCreateArtifactSlug(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "hash-slug", "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateArtifact(ctx, Artifact{ID: "slugless0001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	a, err := s.Artifact(ctx, "slugless0001")
	if err != nil || !slugShape.MatchString(a.Slug) {
		t.Fatalf("a page created without a slug gets one: %v %q", err, a.Slug)
	}
	if err := s.CreateArtifact(ctx, Artifact{ID: "slugged00001", Slug: "givenslug234abcd", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ArtifactBySlug(ctx, "givenslug234abcd"); err != nil || got.ID != "slugged00001" {
		t.Fatalf("by slug: %v %+v", err, got)
	}
	if err := s.CreateArtifact(ctx, Artifact{ID: "slugclash001", Slug: "givenslug234abcd", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("a slug already in use must be refused")
	}
	for _, slug := range []string{"", "nosuchslug234567", "GIVENSLUG234ABCD"} {
		if _, err := s.ArtifactBySlug(ctx, slug); err != ErrNotFound {
			t.Fatalf("%q: want ErrNotFound, got %v", slug, err)
		}
	}
}
