package store

import (
	"context"
	"strings"
)

// CreateArtifact inserts the row. comments_enabled is left to its column
// default (on) so every caller starts with comments; SetCommentsEnabled turns
// them off.
func (s *sqlStore) CreateArtifact(ctx context.Context, a Artifact) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO artifacts(id,key_id,tenant_id,end_user_id,title,current_version,created_at,updated_at) VALUES (?,?,COALESCE(NULLIF(?,''),(SELECT tenant_id FROM keys WHERE id=?)),?,?,?,?,?)`), a.ID, a.KeyID, a.TenantID, a.KeyID, a.EndUserID, a.Title, a.CurrentVersion, a.CreatedAt, a.UpdatedAt)
	return err
}

const artifactCols = `a.id,COALESCE(a.key_id,0),a.tenant_id,a.end_user_id,a.title,a.current_version,a.comments_enabled,a.created_at,a.updated_at,a.deleted_at,(SELECT e.external_id FROM end_users e WHERE e.id=a.end_user_id),(SELECT COUNT(*) FROM threads t WHERE t.artifact_id=a.id AND t.status='open')`

func scanArtifact(row interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	err := row.Scan(&a.ID, &a.KeyID, &a.TenantID, &a.EndUserID, &a.Title, &a.CurrentVersion, &a.CommentsEnabled, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &a.EndUser, &a.OpenComments)
	return a, notFound(err)
}
func (s *sqlStore) Artifact(ctx context.Context, id string) (Artifact, error) {
	return scanArtifact(s.db.QueryRowContext(ctx, s.q(`SELECT `+artifactCols+` FROM artifacts a WHERE a.id=?`), id))
}
func (s *sqlStore) ListArtifacts(ctx context.Context, tenantID string, f ArtifactFilter) ([]Artifact, error) {
	q := `SELECT ` + artifactCols + ` FROM artifacts a WHERE a.tenant_id=? AND a.deleted_at IS NULL`
	args := []any{tenantID}
	if f.EndUser != "" {
		q += ` AND a.end_user_id IN (SELECT id FROM end_users WHERE tenant_id=? AND external_id=?)`
		args = append(args, tenantID, f.EndUser)
	}
	if f.EndUserID != "" {
		q += ` AND a.end_user_id=?`
		args = append(args, f.EndUserID)
	}
	if f.Query != "" {
		q += ` AND LOWER(a.title) LIKE LOWER(?) ESCAPE '\'`
		args = append(args, "%"+strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(f.Query)+"%")
	}
	if !f.Before.IsZero() {
		if f.BeforeID != "" {
			q += ` AND (a.updated_at<? OR (a.updated_at=? AND a.id<?))`
			args = append(args, f.Before, f.Before, f.BeforeID)
		} else {
			q += ` AND a.updated_at<?`
			args = append(args, f.Before)
		}
	}
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	q += ` ORDER BY a.updated_at DESC,a.id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *sqlStore) CountArtifacts(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM artifacts WHERE tenant_id=? AND deleted_at IS NULL`), tenantID).Scan(&n)
	return n, err
}
