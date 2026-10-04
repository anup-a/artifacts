package store

import (
	"context"
	"database/sql"
	"time"
)

const connectCols = `code_sha256,device_sha256,name,tenant_id,email,created_at,expires_at,approved_at,consumed_at`

func scanConnect(row interface{ Scan(...any) error }) (ConnectCode, error) {
	var c ConnectCode
	var device, tenant sql.NullString
	err := row.Scan(&c.CodeSHA256, &device, &c.Name, &tenant, &c.Email, &c.CreatedAt, &c.ExpiresAt, &c.ApprovedAt, &c.ConsumedAt)
	c.DeviceSHA256, c.TenantID = device.String, tenant.String
	return c, notFound(err)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *sqlStore) CreateConnect(ctx context.Context, c ConnectCode) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO connect_codes(code_sha256,device_sha256,name,tenant_id,email,created_at,expires_at,approved_at) VALUES (?,?,?,?,?,?,?,?)`),
		c.CodeSHA256, nullable(c.DeviceSHA256), c.Name, nullable(c.TenantID), c.Email, c.CreatedAt, c.ExpiresAt, c.ApprovedAt)
	return err
}

func (s *sqlStore) ConnectByCode(ctx context.Context, codeHash string, now time.Time) (ConnectCode, error) {
	return scanConnect(s.db.QueryRowContext(ctx, s.q(`SELECT `+connectCols+` FROM connect_codes WHERE code_sha256=? AND expires_at>? AND consumed_at IS NULL`), codeHash, now))
}

func (s *sqlStore) ApproveConnect(ctx context.Context, codeHash, tenantID, email string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE connect_codes SET tenant_id=?, email=?, approved_at=? WHERE code_sha256=? AND device_sha256 IS NOT NULL AND approved_at IS NULL AND consumed_at IS NULL AND expires_at>?`),
		tenantID, email, now, codeHash, now)
	return affected(res, err)
}

// RedeemConnect marks the code spent before it makes the key, inside one
// transaction: two redeems racing for the same code cannot both get a key.
func (s *sqlStore) RedeemConnect(ctx context.Context, hash string, byDevice bool, keyHash, prefix, name string, now time.Time) (Key, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Key{}, err
	}
	defer tx.Rollback()
	where := `code_sha256=? AND device_sha256 IS NULL`
	if byDevice {
		where = `device_sha256=?`
	}
	c, err := scanConnect(tx.QueryRowContext(ctx, s.q(`SELECT `+connectCols+` FROM connect_codes WHERE `+where+` AND expires_at>? AND consumed_at IS NULL`), hash, now))
	if err != nil {
		return Key{}, err
	}
	if c.ApprovedAt == nil || c.TenantID == "" {
		return Key{}, ErrPending
	}
	res, err := tx.ExecContext(ctx, s.q(`UPDATE connect_codes SET consumed_at=? WHERE code_sha256=? AND consumed_at IS NULL`), now, c.CodeSHA256)
	if err := affected(res, err); err != nil {
		return Key{}, err
	}
	if c.Name != "" {
		name = c.Name
	}
	k, err := s.insertKey(ctx, tx, c.TenantID, keyHash, c.Email, name, prefix, now)
	if err != nil {
		return Key{}, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE connect_codes SET key_id=? WHERE code_sha256=?`), k.ID, c.CodeSHA256); err != nil {
		return Key{}, err
	}
	if err := tx.Commit(); err != nil {
		return Key{}, err
	}
	return k, nil
}
