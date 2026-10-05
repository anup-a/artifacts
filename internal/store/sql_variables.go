package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Variable is one account variable as stored: the sealed value, never the
// plain one. AllowedUpstreams is never nil when read back.
type Variable struct {
	TenantID         string
	Name             string
	Sealed           string
	AllowedUpstreams []string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ErrVariableLimit is returned by PutVariable when a new name would take the
// workspace past its limit.
var ErrVariableLimit = errors.New("variable limit reached")

// VariableStore keeps account variables.
type VariableStore interface {
	// PutVariable creates or replaces one variable. A new name past max
	// variables in the workspace fails with ErrVariableLimit. created reports
	// whether the name was new.
	PutVariable(ctx context.Context, v Variable, max int) (created bool, err error)
	// Variables lists a workspace's variables by name.
	Variables(ctx context.Context, tenantID string) ([]Variable, error)
	// DeleteVariable removes one, or returns ErrNotFound.
	DeleteVariable(ctx context.Context, tenantID, name string) error
}

func (s *sqlStore) PutVariable(ctx context.Context, v Variable, max int) (bool, error) {
	hosts, err := json.Marshal(v.AllowedUpstreams)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists, count int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM variables WHERE tenant_id=? AND name=?`), v.TenantID, v.Name).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM variables WHERE tenant_id=?`), v.TenantID).Scan(&count); err != nil {
			return false, err
		}
		if count >= max {
			return false, ErrVariableLimit
		}
	}
	at := v.UpdatedAt.UTC()
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO variables(tenant_id,name,sealed,allowed_upstreams,created_at,updated_at) VALUES (?,?,?,?,?,?)
ON CONFLICT(tenant_id,name) DO UPDATE SET sealed=excluded.sealed, allowed_upstreams=excluded.allowed_upstreams, updated_at=excluded.updated_at`),
		v.TenantID, v.Name, v.Sealed, string(hosts), at, at); err != nil {
		return false, err
	}
	return exists == 0, tx.Commit()
}

func (s *sqlStore) Variables(ctx context.Context, tenantID string) ([]Variable, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT tenant_id,name,sealed,allowed_upstreams,created_at,updated_at FROM variables WHERE tenant_id=? ORDER BY name`), tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Variable{}
	for rows.Next() {
		var v Variable
		var hosts sql.NullString
		if err := rows.Scan(&v.TenantID, &v.Name, &v.Sealed, &hosts, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if v.AllowedUpstreams, err = decodeTags(hosts); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteVariable(ctx context.Context, tenantID, name string) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM variables WHERE tenant_id=? AND name=?`), tenantID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
