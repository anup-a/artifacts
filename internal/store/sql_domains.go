package store

import (
	"context"
	"database/sql"
	"errors"
)

const domainCols = `hostname,tenant_id,token,status,ssl_status,provider,provider_ref,message,verified_at,checked_at,created_at`

func scanDomain(row interface{ Scan(...any) error }) (Domain, error) {
	var d Domain
	err := row.Scan(&d.Hostname, &d.TenantID, &d.Token, &d.Status, &d.SSLStatus, &d.Provider, &d.ProviderRef, &d.Message, &d.VerifiedAt, &d.CheckedAt, &d.CreatedAt)
	return d, notFound(err)
}

// CreateDomain checks the hostname and the workspace's count and inserts, in
// one transaction, so two adds of the same name cannot both win.
func (s *sqlStore) CreateDomain(ctx context.Context, d Domain, max int) (Domain, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Domain{}, err
	}
	defer tx.Rollback()
	existing, err := scanDomain(tx.QueryRowContext(ctx, s.q(`SELECT `+domainCols+` FROM domains WHERE hostname=?`), d.Hostname))
	if err == nil {
		return existing, ErrDomainTaken
	}
	if !errors.Is(err, ErrNotFound) {
		return Domain{}, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM domains WHERE tenant_id=?`), d.TenantID).Scan(&n); err != nil {
		return Domain{}, err
	}
	if n >= max {
		return Domain{}, ErrDomainQuota
	}
	if d.Status == "" {
		d.Status = "pending"
	}
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO domains(`+domainCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		d.Hostname, d.TenantID, d.Token, d.Status, d.SSLStatus, d.Provider, d.ProviderRef, d.Message, d.VerifiedAt, d.CheckedAt, d.CreatedAt); err != nil {
		return Domain{}, err
	}
	return d, tx.Commit()
}

func (s *sqlStore) Domain(ctx context.Context, hostname string) (Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, s.q(`SELECT `+domainCols+` FROM domains WHERE hostname=?`), hostname))
}

func (s *sqlStore) Domains(ctx context.Context, tenantID string) ([]Domain, error) {
	var rows *sql.Rows
	var err error
	if tenantID == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT `+domainCols+` FROM domains ORDER BY created_at, hostname`)
	} else {
		rows, err = s.db.QueryContext(ctx, s.q(`SELECT `+domainCols+` FROM domains WHERE tenant_id=? ORDER BY created_at, hostname`), tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *sqlStore) UpdateDomainState(ctx context.Context, d Domain) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE domains SET status=?, ssl_status=?, message=?, provider=?, provider_ref=?, verified_at=?, checked_at=? WHERE hostname=?`),
		d.Status, d.SSLStatus, d.Message, d.Provider, d.ProviderRef, d.VerifiedAt, d.CheckedAt, d.Hostname)
	return affected(res, err)
}

func (s *sqlStore) DeleteDomain(ctx context.Context, hostname string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM domain_mounts WHERE hostname=?`), hostname); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, s.q(`DELETE FROM domains WHERE hostname=?`), hostname)
	if err := affected(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

const mountCols = `hostname,path,artifact_id,is_primary,created_at`

func scanMounts(rows *sql.Rows) ([]Mount, error) {
	defer rows.Close()
	out := []Mount{}
	for rows.Next() {
		var m Mount
		var primary int
		if err := rows.Scan(&m.Hostname, &m.Path, &m.ArtifactID, &primary, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Primary = primary == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *sqlStore) Mounts(ctx context.Context, hostname string) ([]Mount, error) {
	if hostname == "" {
		rows, err := s.db.QueryContext(ctx, `SELECT `+mountCols+` FROM domain_mounts ORDER BY hostname, path`)
		if err != nil {
			return nil, err
		}
		return scanMounts(rows)
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+mountCols+` FROM domain_mounts WHERE hostname=? ORDER BY path`), hostname)
	if err != nil {
		return nil, err
	}
	return scanMounts(rows)
}

func (s *sqlStore) ArtifactMounts(ctx context.Context, artifactID string) ([]Mount, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+mountCols+` FROM domain_mounts WHERE artifact_id=? ORDER BY hostname, path`), artifactID)
	if err != nil {
		return nil, err
	}
	return scanMounts(rows)
}

// PutMount upserts by (hostname, path). Pointing a mount at a different
// artifact clears its primary mark: the new artifact did not ask for it.
func (s *sqlStore) PutMount(ctx context.Context, m Mount) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO domain_mounts(`+mountCols+`) VALUES (?,?,?,0,?)
		ON CONFLICT (hostname,path) DO UPDATE SET artifact_id=excluded.artifact_id,
		is_primary=CASE WHEN domain_mounts.artifact_id=excluded.artifact_id THEN domain_mounts.is_primary ELSE 0 END`),
		m.Hostname, m.Path, m.ArtifactID, m.CreatedAt)
	return err
}

func (s *sqlStore) DeleteMount(ctx context.Context, hostname, path string) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM domain_mounts WHERE hostname=? AND path=?`), hostname, path)
	return affected(res, err)
}

func (s *sqlStore) SetPrimaryMount(ctx context.Context, artifactID, hostname, path string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE domain_mounts SET is_primary=0 WHERE artifact_id=?`), artifactID); err != nil {
		return err
	}
	if hostname != "" {
		res, err := tx.ExecContext(ctx, s.q(`UPDATE domain_mounts SET is_primary=1 WHERE artifact_id=? AND hostname=? AND path=?`), artifactID, hostname, path)
		if err := affected(res, err); err != nil {
			return err
		}
	}
	return tx.Commit()
}
