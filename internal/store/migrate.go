package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"github.com/anup-a/byagent/internal/ids"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// runMigrations applies embedded migrations in filename order. rewrite adapts
// the `?` placeholders in the bookkeeping statements to the target dialect
// (identity for sqlite, `$n` for postgres); without it Postgres rejects `?`.
func runMigrations(ctx context.Context, db *sql.DB, rewrite func(string) string) error {
	return migrateTo(ctx, db, rewrite, 0)
}

// migrateTo applies migrations up to and including version upTo, or all of
// them when upTo is 0. Tests stop short of a migration to load the rows it
// then has to carry forward.
func migrateTo(ctx context.Context, db *sql.DB, rewrite func(string) string, upTo int) error {
	dialect := "sqlite"
	if rewrite("?") != "?" {
		dialect = "postgres"
	}
	if dialect == "sqlite" {
		// SQLite table replacement requires foreign keys disabled outside the transaction.
		if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			return err
		}
		defer db.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		ver, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration name %q: %w", name, err)
		}
		if upTo > 0 && ver > upTo {
			break
		}
		var n int
		if err := db.QueryRowContext(ctx, rewrite(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`), ver).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrationSQL(string(body), dialect)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if ver == 2 {
			if err := backfillTenants(ctx, tx, rewrite); err != nil {
				tx.Rollback()
				return err
			}
		}
		if ver == slugMigration {
			if err := backfillSlugs(ctx, tx, rewrite); err != nil {
				tx.Rollback()
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, rewrite(`INSERT INTO schema_migrations(version) VALUES (?)`), ver); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func migrationSQL(body, dialect string) string {
	var out strings.Builder
	active := true
	for _, line := range strings.Split(body, "\n") {
		if section, ok := strings.CutPrefix(line, "-- dialect:"); ok {
			active = section == "all" || section == dialect
			continue
		}
		if active {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// slugMigration is 0019_page_slugs, which backfillSlugs completes.
const slugMigration = 19

// backfillSlugs gives every artifact without a slug a fresh one. It runs in
// the migration's transaction, and only touches rows still NULL, so running
// it again is harmless.
func backfillSlugs(ctx context.Context, tx *sql.Tx, q func(string) string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM artifacts WHERE slug IS NULL`)
	if err != nil {
		return err
	}
	var missing []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range missing {
		if _, err := tx.ExecContext(ctx, q(`UPDATE artifacts SET slug=? WHERE id=? AND slug IS NULL`), ids.NewSlug(), id); err != nil {
			return fmt.Errorf("slug for %s: %w", id, err)
		}
	}
	return nil
}

func backfillTenants(ctx context.Context, tx *sql.Tx, q func(string) string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,email,created_at FROM keys`)
	if err != nil {
		return err
	}
	var keys []Key
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.Email, &k.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range keys {
		tenantID := "t_" + ids.NewArtifactID()
		if k.Email == "admin@local" {
			tenantID = "admin"
		}
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO tenants(id,kind,name,created_at) VALUES (?,'personal',?,?) ON CONFLICT(id) DO NOTHING`), tenantID, k.Email, k.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q(`UPDATE keys SET tenant_id=? WHERE id=?`), tenantID, k.ID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE artifacts SET tenant_id=(SELECT tenant_id FROM keys WHERE keys.id=artifacts.key_id)`)
	return err
}
