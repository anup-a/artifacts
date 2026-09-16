package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/ids"
)

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	return u, notFound(err)
}

// SignIn accepts only a verified identity. New identities with the same email
// share a user and personal tenant, independently of their provider.
func (s *sqlStore) SignIn(ctx context.Context, provider, subject, email, name string, now time.Time) (User, Tenant, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, Tenant{}, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, s.q(`SELECT u.id,u.email,u.name,u.created_at FROM users u JOIN identities i ON i.user_id=u.id WHERE i.provider=? AND i.subject=?`), provider, subject))
	if errors.Is(err, ErrNotFound) {
		u, err = s.linkIdentity(ctx, tx, provider, subject, strings.ToLower(strings.TrimSpace(email)), name, now)
	}
	if err != nil {
		return User{}, Tenant{}, err
	}
	tenant, err := scanTenant(tx.QueryRowContext(ctx, s.q(`SELECT t.id,t.kind,t.name,t.plan,t.created_at FROM tenants t JOIN memberships m ON m.tenant_id=t.id WHERE m.user_id=? ORDER BY CASE WHEN t.kind='personal' THEN 0 ELSE 1 END,t.created_at,t.id LIMIT 1`), u.ID))
	if err != nil {
		return User{}, Tenant{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, Tenant{}, err
	}
	return u, tenant, nil
}

func (s *sqlStore) linkIdentity(ctx context.Context, tx *sql.Tx, provider, subject, email, name string, now time.Time) (User, error) {
	u := User{ID: "u_" + ids.NewArtifactID(), Email: email, Name: name, CreatedAt: now}
	result, err := tx.ExecContext(ctx, s.q(`INSERT INTO users(id,email,name,created_at) VALUES (?,?,?,?) ON CONFLICT(email) DO NOTHING`), u.ID, u.Email, u.Name, now)
	if err != nil {
		return User{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return User{}, err
	}
	if inserted > 0 {
		tenantID := "t_" + ids.NewArtifactID()
		local, _, _ := strings.Cut(email, "@")
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO tenants(id,kind,name,plan,created_at) VALUES (?,'personal',?,'free',?)`), tenantID, local, now); err != nil {
			return User{}, err
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO memberships(tenant_id,user_id,role,created_at) VALUES (?,?,'owner',?)`), tenantID, u.ID, now); err != nil {
			return User{}, err
		}
	} else {
		u, err = scanUser(tx.QueryRowContext(ctx, s.q(`SELECT id,email,name,created_at FROM users WHERE email=?`), email))
		if err != nil {
			return User{}, err
		}
	}
	_, err = tx.ExecContext(ctx, s.q(`INSERT INTO identities(provider,subject,user_id,created_at) VALUES (?,?,?,?) ON CONFLICT(provider,subject) DO NOTHING`), provider, subject, u.ID, now)
	if err != nil {
		return User{}, err
	}
	// Another verified callback may have linked the identity while this request
	// waited on the unique email constraint. Always return its canonical user.
	canonical, err := scanUser(tx.QueryRowContext(ctx, s.q(`SELECT u.id,u.email,u.name,u.created_at FROM users u JOIN identities i ON i.user_id=u.id WHERE i.provider=? AND i.subject=?`), provider, subject))
	if err != nil {
		return User{}, err
	}
	if canonical.ID != u.ID {
		// Rolling back avoids committing an unused user or tenant when two
		// callbacks for the same provider identity carry different emails.
		return User{}, errors.New("identity linked concurrently; try signing in again")
	}
	return canonical, nil
}

func (s *sqlStore) User(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, s.q(`SELECT id,email,name,created_at FROM users WHERE id=?`), id))
}

func (s *sqlStore) Identities(ctx context.Context, userID string) ([]Identity, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT provider,subject,user_id,created_at FROM identities WHERE user_id=? ORDER BY provider,subject`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Identity{}
	for rows.Next() {
		var i Identity
		if err := rows.Scan(&i.Provider, &i.Subject, &i.UserID, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *sqlStore) CreateSession(ctx context.Context, session Session) error {
	return affected(s.db.ExecContext(ctx, s.q(`INSERT INTO sessions(id,user_id,tenant_id,created_at,expires_at,last_seen_at) SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM memberships WHERE tenant_id=? AND user_id=?)`), session.ID, session.UserID, session.TenantID, session.CreatedAt, session.ExpiresAt, session.LastSeenAt, session.TenantID, session.UserID))
}

func (s *sqlStore) Session(ctx context.Context, id string) (Session, error) {
	var session Session
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id,user_id,tenant_id,created_at,expires_at,last_seen_at FROM sessions WHERE id=?`), id).Scan(&session.ID, &session.UserID, &session.TenantID, &session.CreatedAt, &session.ExpiresAt, &session.LastSeenAt)
	return session, notFound(err)
}

func (s *sqlStore) RefreshSession(ctx context.Context, id string, now, expires time.Time) error {
	return affected(s.db.ExecContext(ctx, s.q(`UPDATE sessions SET last_seen_at=?,expires_at=? WHERE id=? AND expires_at>?`), now, expires, id, now))
}

func (s *sqlStore) SwitchTenant(ctx context.Context, id, userID, tenantID string) error {
	return affected(s.db.ExecContext(ctx, s.q(`UPDATE sessions SET tenant_id=? WHERE id=? AND user_id=? AND EXISTS(SELECT 1 FROM memberships WHERE tenant_id=? AND user_id=?)`), tenantID, id, userID, tenantID, userID))
}

func (s *sqlStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE id=?`), id)
	return err
}

func (s *sqlStore) CreateMagicLink(ctx context.Context, link MagicLink) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO magic_links(token_sha256,email,created_at,expires_at,consumed_at) VALUES (?,?,?,?,?)`), link.TokenSHA256, link.Email, link.CreatedAt, link.ExpiresAt, link.ConsumedAt)
	return err
}

func (s *sqlStore) ConsumeMagicLink(ctx context.Context, hash string, now time.Time) (MagicLink, error) {
	var link MagicLink
	err := s.db.QueryRowContext(ctx, s.q(`UPDATE magic_links SET consumed_at=? WHERE token_sha256=? AND consumed_at IS NULL AND expires_at>? RETURNING token_sha256,email,created_at,expires_at,consumed_at`), now, hash, now).Scan(&link.TokenSHA256, &link.Email, &link.CreatedAt, &link.ExpiresAt, &link.ConsumedAt)
	return link, notFound(err)
}

func (s *sqlStore) CreateOAuthState(ctx context.Context, state string, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO oauth_states(state,created_at,expires_at) VALUES (?,?,?)`), state, now, expires)
	return err
}

func (s *sqlStore) ConsumeOAuthState(ctx context.Context, state string, now time.Time) error {
	return affected(s.db.ExecContext(ctx, s.q(`DELETE FROM oauth_states WHERE state=? AND expires_at>?`), state, now))
}
