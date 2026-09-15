package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anup-a/artifacts/internal/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Open returns a Store for cfg.DB. Call Migrate before use.
func Open(ctx context.Context, cfg config.Config) (Store, error) {
	switch cfg.DB {
	case "sqlite":
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return nil, err
		}
		dsn := "file:" + filepath.Join(cfg.DataDir, "artifacts.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1) // sqlite: serialize writers
		return &sqlStore{db: db, dialect: "sqlite"}, nil
	case "postgres":
		db, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		return &sqlStore{db: db, dialect: "postgres"}, nil
	}
	return nil, fmt.Errorf("unknown DB %q", cfg.DB)
}
