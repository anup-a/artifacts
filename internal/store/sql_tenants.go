package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/anup-a/artifacts/internal/ids"
)

func scanTenant(row interface{ Scan(...any) error }) (Tenant, error) {
	var t Tenant
	err := row.Scan(&t.ID, &t.Kind, &t.Name, &t.Plan, &t.CreatedAt)
	return t, notFound(err)
}

func (s *sqlStore) Tenant(ctx context.Context, id string) (Tenant, error) {
	return scanTenant(s.db.QueryRowContext(ctx, s.q(`SELECT id,kind,name,plan,created_at FROM tenants WHERE id=?`), id))
}

func (s *sqlStore) CreateTenant(ctx context.Context, t Tenant, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if t.Plan == "" {
		t.Plan = "free"
	}
	if _, err = tx.ExecContext(ctx, s.q(`INSERT INTO tenants(id,kind,name,plan,created_at) VALUES (?,?,?,?,?)`), t.ID, t.Kind, t.Name, t.Plan, t.CreatedAt); err != nil {
		return err
	}
	if userID != "" {
		if _, err = tx.ExecContext(ctx, s.q(`INSERT INTO memberships(tenant_id,user_id,role,created_at) VALUES (?,?,'owner',?)`), t.ID, userID, t.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqlStore) RenameTenant(ctx context.Context, id, name string) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE tenants SET name=? WHERE id=?`), name, id)
	return affected(res, err)
}

func (s *sqlStore) TenantsForUser(ctx context.Context, userID string) ([]Tenant, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT t.id,t.kind,t.name,t.plan,t.created_at FROM tenants t JOIN memberships m ON m.tenant_id=t.id WHERE m.user_id=? ORDER BY t.created_at,t.id`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const keyCols = `id,tenant_id,email,name,prefix,created_at,last_used_at,revoked_at`

func scanKey(row interface{ Scan(...any) error }) (Key, error) {
	var k Key
	err := row.Scan(&k.ID, &k.TenantID, &k.Email, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	return k, notFound(err)
}
func (s *sqlStore) KeyByHash(ctx context.Context, hash string) (Key, error) {
	return scanKey(s.db.QueryRowContext(ctx, s.q(`SELECT `+keyCols+` FROM keys WHERE key_sha256=?`), hash))
}

// CreateKey bootstraps an API-only key with its personal tenant. Web-created
// keys use CreateTenantKey and therefore share their workspace's ownership.
func (s *sqlStore) CreateKey(ctx context.Context, hash, email string) (Key, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Key{}, err
	}
	defer tx.Rollback()
	tenantID := "t_" + ids.NewArtifactID()
	if email == "admin@local" {
		tenantID = "admin"
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, s.q(`INSERT INTO tenants(id,kind,name,created_at) VALUES (?,'personal',?,?) ON CONFLICT(id) DO NOTHING`), tenantID, email, now); err != nil {
		return Key{}, err
	}
	k, err := s.insertKey(ctx, tx, tenantID, hash, email, "", "", now)
	if err != nil {
		return k, err
	}
	return k, tx.Commit()
}

func (s *sqlStore) CreateTenantKey(ctx context.Context, tenantID, hash, email, name, prefix string, now time.Time) (Key, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Key{}, err
	}
	defer tx.Rollback()
	k, err := s.insertKey(ctx, tx, tenantID, hash, email, name, prefix, now)
	if err != nil {
		return k, err
	}
	return k, tx.Commit()
}

func (s *sqlStore) insertKey(ctx context.Context, tx *sql.Tx, tenantID, hash, email, name, prefix string, now time.Time) (Key, error) {
	if s.dialect == "postgres" {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE keys IN EXCLUSIVE MODE`); err != nil {
			return Key{}, err
		}
	}
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0)+1 FROM keys`).Scan(&next); err != nil {
		return Key{}, err
	}
	_, err := tx.ExecContext(ctx, s.q(`INSERT INTO keys(id,tenant_id,key_sha256,email,name,prefix,created_at) VALUES (?,?,?,?,?,?,?)`), next, tenantID, hash, email, name, prefix, now)
	return Key{ID: next, TenantID: tenantID, Email: email, Name: name, Prefix: prefix, CreatedAt: now}, err
}

func (s *sqlStore) Keys(ctx context.Context, tenantID string, now time.Time) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+keyCols+` FROM keys WHERE tenant_id=? AND (revoked_at IS NULL OR revoked_at>?) ORDER BY created_at DESC,id DESC`), tenantID, now.Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *sqlStore) RevokeKey(ctx context.Context, tenantID string, id int64, now time.Time) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE keys SET revoked_at=COALESCE(revoked_at,?) WHERE id=? AND tenant_id=?`), now, id, tenantID)
	return affected(res, err)
}
func (s *sqlStore) TouchKey(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE keys SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at<=?)`), now, id, now.Add(-time.Minute))
	return err
}

const endUserCols = `e.id,e.tenant_id,e.external_id,e.first_seen_at,e.last_seen_at,(SELECT COUNT(*) FROM artifacts a WHERE a.end_user_id=e.id AND a.tenant_id=e.tenant_id AND a.deleted_at IS NULL)`

func scanEndUser(row interface{ Scan(...any) error }) (EndUser, error) {
	var eu EndUser
	err := row.Scan(&eu.ID, &eu.TenantID, &eu.ExternalID, &eu.FirstSeenAt, &eu.LastSeenAt, &eu.ArtifactCount)
	return eu, notFound(err)
}
func (s *sqlStore) UpsertEndUser(ctx context.Context, tenantID, externalID string, now time.Time) (EndUser, error) {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO end_users(id,tenant_id,external_id,first_seen_at,last_seen_at) VALUES (?,?,?,?,?) ON CONFLICT(tenant_id,external_id) DO UPDATE SET last_seen_at=excluded.last_seen_at`), "eu_"+ids.NewArtifactID(), tenantID, externalID, now, now)
	if err != nil {
		return EndUser{}, err
	}
	return s.EndUser(ctx, tenantID, externalID)
}
func (s *sqlStore) EndUser(ctx context.Context, tenantID, externalID string) (EndUser, error) {
	return scanEndUser(s.db.QueryRowContext(ctx, s.q(`SELECT `+endUserCols+` FROM end_users e WHERE e.tenant_id=? AND e.external_id=?`), tenantID, externalID))
}
func (s *sqlStore) EndUserByID(ctx context.Context, tenantID, id string) (EndUser, error) {
	return scanEndUser(s.db.QueryRowContext(ctx, s.q(`SELECT `+endUserCols+` FROM end_users e WHERE e.tenant_id=? AND e.id=?`), tenantID, id))
}
func (s *sqlStore) EndUsers(ctx context.Context, tenantID string) ([]EndUser, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+endUserCols+` FROM end_users e WHERE e.tenant_id=? ORDER BY e.last_seen_at DESC,e.id`), tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EndUser{}
	for rows.Next() {
		e, err := scanEndUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteExpiredCredentials(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []struct {
		q      string
		cutoff time.Time
	}{
		{`DELETE FROM keys WHERE revoked_at<=?`, now.Add(-30 * 24 * time.Hour)},
		{`DELETE FROM sessions WHERE expires_at<=?`, now},
		{`DELETE FROM magic_links WHERE expires_at<=?`, now},
		{`DELETE FROM oauth_states WHERE expires_at<=?`, now},
		{`DELETE FROM connect_codes WHERE expires_at<=?`, now},
	}
	for _, q := range queries {
		if _, err = tx.ExecContext(ctx, s.q(q.q), q.cutoff); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// claimOrphanAdmin moves bootstrap-admin artifacts and keys onto the first
// signed-in personal tenant. GitHub/email sign-in used to mint a second
// personal workspace, so CLI publishes (ADMIN_TOKEN → admin) vanished from
// the dashboard. Idempotent: no-op if admin already has a member or there is
// no other personal tenant.
func (s *sqlStore) claimOrphanAdmin(ctx context.Context) error {
	var members int
	if err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM memberships WHERE tenant_id='admin'`)).Scan(&members); err != nil {
		return err
	}
	if members > 0 {
		return nil
	}
	var dest string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT t.id FROM tenants t JOIN memberships m ON m.tenant_id=t.id WHERE t.kind='personal' AND t.id!='admin' ORDER BY t.created_at, t.id LIMIT 1`)).Scan(&dest)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, s.q(`SELECT id, external_id FROM end_users WHERE tenant_id='admin'`))
	if err != nil {
		return err
	}
	type eu struct{ id, ext string }
	var adminEUs []eu
	for rows.Next() {
		var e eu
		if err := rows.Scan(&e.id, &e.ext); err != nil {
			rows.Close()
			return err
		}
		adminEUs = append(adminEUs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range adminEUs {
		var destID string
		err := tx.QueryRowContext(ctx, s.q(`SELECT id FROM end_users WHERE tenant_id=? AND external_id=?`), dest, e.ext).Scan(&destID)
		switch {
		case err == nil:
			if _, err := tx.ExecContext(ctx, s.q(`UPDATE artifacts SET end_user_id=? WHERE end_user_id=?`), destID, e.id); err != nil {
				return err
			}
		case err == sql.ErrNoRows:
			if _, err := tx.ExecContext(ctx, s.q(`UPDATE end_users SET tenant_id=? WHERE id=?`), dest, e.id); err != nil {
				return err
			}
		default:
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE artifacts SET tenant_id=? WHERE tenant_id='admin'`), dest); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE keys SET tenant_id=? WHERE tenant_id='admin'`), dest); err != nil {
		return err
	}
	return tx.Commit()
}
