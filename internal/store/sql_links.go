package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Link is a page an agent published somewhere other than byagent and
// registered so it shows up in the workspace next to hosted pages. It is a
// pointer only: byagent stores no files for it and never serves it.
type Link struct {
	ID        string
	TenantID  string
	URL       string
	Host      string
	Title     string
	Project   *string
	Agent     string
	Model     string
	KeyName   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LinkFilter narrows ListLinks the way ArtifactFilter narrows the page list.
type LinkFilter struct {
	Project string
	Query   string // case-insensitive match on title or URL
	Limit   int
}

// ErrLinkLimit is returned by PutLink when a new URL would take the
// workspace past its limit.
var ErrLinkLimit = errors.New("link limit reached")

// LinkStore keeps links.
type LinkStore interface {
	// PutLink registers l.URL in l.TenantID, or refreshes the link already
	// there: updated_at moves, and title, project, agent and model change
	// only where l carries a value, so a bare re-register keeps the labels.
	// A new URL past max links fails with ErrLinkLimit. It returns the stored
	// link and whether it was new; l.ID is used only for a new one.
	PutLink(ctx context.Context, l Link, max int) (Link, bool, error)
	ListLinks(ctx context.Context, tenantID string, f LinkFilter) ([]Link, error)
	// LinkByID and LinkByURL find a link in one workspace, or ErrNotFound.
	LinkByID(ctx context.Context, tenantID, id string) (Link, error)
	LinkByURL(ctx context.Context, tenantID, url string) (Link, error)
	// DeleteLink removes one, or returns ErrNotFound.
	DeleteLink(ctx context.Context, tenantID, id string) error
	CountLinks(ctx context.Context, tenantID string) (int, error)
}

const linkCols = `id,tenant_id,url,host,title,project,agent,model,key_name,created_at,updated_at`

func scanLink(row interface{ Scan(...any) error }) (Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.TenantID, &l.URL, &l.Host, &l.Title, &l.Project, &l.Agent, &l.Model, &l.KeyName, &l.CreatedAt, &l.UpdatedAt)
	return l, notFound(err)
}

func (s *sqlStore) PutLink(ctx context.Context, l Link, max int) (Link, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Link{}, false, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM links WHERE tenant_id=? AND url=?`), l.TenantID, l.URL).Scan(&exists); err != nil {
		return Link{}, false, err
	}
	if exists == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM links WHERE tenant_id=?`), l.TenantID).Scan(&count); err != nil {
			return Link{}, false, err
		}
		if count >= max {
			return Link{}, false, ErrLinkLimit
		}
	}
	at := l.UpdatedAt.UTC()
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO links(id,tenant_id,url,host,title,project,agent,model,key_name,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(tenant_id,url) DO UPDATE SET updated_at=excluded.updated_at, key_name=excluded.key_name,
 title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE links.title END,
 project=COALESCE(excluded.project, links.project),
 agent=CASE WHEN excluded.agent<>'' THEN excluded.agent ELSE links.agent END,
 model=CASE WHEN excluded.model<>'' THEN excluded.model ELSE links.model END`),
		l.ID, l.TenantID, l.URL, l.Host, l.Title, l.Project, l.Agent, l.Model, l.KeyName, at, at); err != nil {
		return Link{}, false, err
	}
	got, err := scanLink(tx.QueryRowContext(ctx, s.q(`SELECT `+linkCols+` FROM links WHERE tenant_id=? AND url=?`), l.TenantID, l.URL))
	if err != nil {
		return Link{}, false, err
	}
	return got, exists == 0, tx.Commit()
}

func (s *sqlStore) ListLinks(ctx context.Context, tenantID string, f LinkFilter) ([]Link, error) {
	q := `SELECT ` + linkCols + ` FROM links WHERE tenant_id=?`
	args := []any{tenantID}
	if f.Project != "" {
		q += ` AND project=?`
		args = append(args, f.Project)
	}
	if f.Query != "" {
		like := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(f.Query) + "%"
		q += ` AND (LOWER(title) LIKE LOWER(?) ESCAPE '\' OR LOWER(url) LIKE LOWER(?) ESCAPE '\')`
		args = append(args, like, like)
	}
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	q += ` ORDER BY updated_at DESC,id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *sqlStore) LinkByID(ctx context.Context, tenantID, id string) (Link, error) {
	return scanLink(s.db.QueryRowContext(ctx, s.q(`SELECT `+linkCols+` FROM links WHERE tenant_id=? AND id=?`), tenantID, id))
}

func (s *sqlStore) LinkByURL(ctx context.Context, tenantID, url string) (Link, error) {
	return scanLink(s.db.QueryRowContext(ctx, s.q(`SELECT `+linkCols+` FROM links WHERE tenant_id=? AND url=?`), tenantID, url))
}

func (s *sqlStore) DeleteLink(ctx context.Context, tenantID, id string) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM links WHERE tenant_id=? AND id=?`), tenantID, id)
	return affected(res, err)
}

func (s *sqlStore) CountLinks(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM links WHERE tenant_id=?`), tenantID).Scan(&n)
	return n, err
}
