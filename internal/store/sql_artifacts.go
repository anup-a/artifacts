package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/guest"
	"github.com/anup-a/artifacts/internal/ids"
)

// CreateArtifact inserts the row. comments_enabled is left to its column
// default (on) so every caller starts with comments; SetCommentsEnabled turns
// them off. The gallery labels come from the struct, nil tags stored as NULL.
func (s *sqlStore) CreateArtifact(ctx context.Context, a Artifact) error {
	if a.Visibility == "" {
		a.Visibility = "public"
	}
	if err := validateVisibility(a.Visibility); err != nil {
		return err
	}
	tags, err := encodeTags(a.Tags)
	if err != nil {
		return err
	}
	if a.Slug == "" {
		a.Slug = ids.NewSlug()
	}
	_, err = s.db.ExecContext(ctx, s.q(`INSERT INTO artifacts(id,key_id,tenant_id,end_user_id,title,current_version,project,tags,cover_path,cover_style,created_at,updated_at,visibility,expires_at,slug) VALUES (?,?,COALESCE(NULLIF(?,''),(SELECT tenant_id FROM keys WHERE id=?)),?,?,?,?,?,?,?,?,?,?,?,?)`),
		a.ID, a.KeyID, a.TenantID, a.KeyID, a.EndUserID, a.Title, a.CurrentVersion, a.Project, tags, a.CoverPath, a.CoverStyle, a.CreatedAt, a.UpdatedAt, a.Visibility, a.ExpiresAt, a.Slug)
	return err
}

const artifactCols = `a.id,COALESCE(a.key_id,0),a.tenant_id,a.end_user_id,a.title,a.current_version,a.comments_enabled,a.project,a.tags,a.cover_path,a.cover_style,a.created_at,a.updated_at,a.deleted_at,(SELECT e.external_id FROM end_users e WHERE e.id=a.end_user_id),(SELECT COUNT(*) FROM threads t WHERE t.artifact_id=a.id AND t.status='open'),a.visibility,a.share_code,a.expires_at,a.share_code_expires_at,a.access_mode,a.access_password,a.access_allow,a.access_version,COALESCE(a.slug,''),a.state_enabled`

func scanArtifact(row interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	var tags sql.NullString
	var allow string
	err := row.Scan(&a.ID, &a.KeyID, &a.TenantID, &a.EndUserID, &a.Title, &a.CurrentVersion, &a.CommentsEnabled, &a.Project, &tags, &a.CoverPath, &a.CoverStyle, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &a.EndUser, &a.OpenComments, &a.Visibility, &a.ShareCode, &a.ExpiresAt, &a.ShareCodeExpiresAt,
		&a.AccessMode, &a.AccessPassword, &allow, &a.AccessVersion, &a.Slug, &a.StateEnabled)
	if err != nil {
		return a, notFound(err)
	}
	if a.AccessAllow, err = decodeAllow(allow); err != nil {
		return a, err
	}
	// An expired guest page reads as deleted before the GC gets to it, so
	// every path that already refuses deleted artifacts refuses it too.
	if a.ShareCodeExpiresAt != nil && !time.Now().Before(*a.ShareCodeExpiresAt) {
		a.ShareCode = ""
	}
	if a.ExpiresAt != nil && a.DeletedAt == nil && !time.Now().Before(*a.ExpiresAt) {
		at := *a.ExpiresAt
		a.DeletedAt = &at
	}
	a.Tags, err = decodeTags(tags)
	return a, err
}

func validateVisibility(visibility string) error {
	if visibility != "public" && visibility != "private" {
		return fmt.Errorf("visibility must be public or private")
	}
	return nil
}

// SetVisibility changes access without changing the content timestamp.
func (s *sqlStore) SetVisibility(ctx context.Context, id, visibility string) error {
	if err := validateVisibility(visibility); err != nil {
		return err
	}
	q := `UPDATE artifacts SET visibility=? WHERE id=?`
	if visibility == "public" {
		// A public page has no door, so going public takes the password and
		// allowlist with it rather than leaving them to come back on the next
		// switch to private.
		q = `UPDATE artifacts SET visibility=?, access_version=access_version+(CASE WHEN access_mode<>'' THEN 1 ELSE 0 END), access_mode='', access_password='', access_allow='' WHERE id=?`
	}
	res, err := s.db.ExecContext(ctx, s.q(q), visibility, id)
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

// SetShareCode stores or clears an artifact's share code. An empty code
// revokes every link that carried the old one, and only for this artifact.
// Like SetVisibility it leaves the content timestamp alone.
func (s *sqlStore) SetShareCode(ctx context.Context, id, code string, expires *time.Time) error {
	if code == "" {
		expires = nil
	}
	expires = utc(expires)
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET share_code=?, share_code_expires_at=? WHERE id=?`), code, expires, id)
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

// encodeTags stores an empty tag list as NULL so "no tags" has one form.
func encodeTags(tags []string) (*string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

// decodeTags reads NULL, "" and "[]" all back as an empty, non-nil slice.
func decodeTags(raw sql.NullString) ([]string, error) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []string{}, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw.String), &tags); err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}

func (s *sqlStore) Artifact(ctx context.Context, id string) (Artifact, error) {
	return scanArtifact(s.db.QueryRowContext(ctx, s.q(`SELECT `+artifactCols+` FROM artifacts a WHERE a.id=?`), id))
}

func (s *sqlStore) ArtifactBySlug(ctx context.Context, slug string) (Artifact, error) {
	if slug == "" {
		return Artifact{}, ErrNotFound
	}
	return scanArtifact(s.db.QueryRowContext(ctx, s.q(`SELECT `+artifactCols+` FROM artifacts a WHERE a.slug=?`), slug))
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
	if f.Project != "" {
		q += ` AND a.project=?`
		args = append(args, f.Project)
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

func (s *sqlStore) Totals(ctx context.Context, now time.Time) (Totals, error) {
	var t Totals
	err := s.db.QueryRowContext(ctx, s.q(`SELECT
		(SELECT COUNT(*) FROM users),
		(SELECT COUNT(*) FROM tenants WHERE plan IN (?,?)),
		(SELECT COUNT(*) FROM artifacts WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at>?)),
		(SELECT COUNT(*) FROM artifacts WHERE deleted_at IS NOT NULL OR expires_at<=?)`), guest.Plan, guest.ClaimedPlan, now, now).
		Scan(&t.Users, &t.Guests, &t.LivePages, &t.DeletedPages)
	return t, err
}

// Erase removes everything a deleted artifact still holds: its comments and
// threads, its page state, its view counts and analytics, every version (which releases the blobs
// to GC) and the labels on the row. The row itself stays, emptied, so the
// id keeps answering 410 and is never handed out again.
func (s *sqlStore) Erase(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM comments WHERE thread_id IN (SELECT id FROM threads WHERE artifact_id=?)`,
		`DELETE FROM threads WHERE artifact_id=?`,
		`DELETE FROM artifact_views WHERE artifact_id=?`,
		`DELETE FROM artifact_view_hours WHERE artifact_id=?`,
		`DELETE FROM artifact_view_facets WHERE artifact_id=?`,
		`DELETE FROM domain_mounts WHERE artifact_id=?`,
		`UPDATE artifacts SET title='', project=NULL, tags=NULL, cover_path=NULL, cover_style='', share_code='' WHERE id=? AND deleted_at IS NOT NULL`,
		`UPDATE artifacts SET access_password='', access_allow='' WHERE id=? AND deleted_at IS NOT NULL`,
		`DELETE FROM access_codes WHERE artifact_id=?`,
		`DELETE FROM page_state WHERE artifact_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, s.q(q), id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// With no threads left, nothing protects a version from pruning.
	_, err = s.PruneVersions(ctx, id, 0)
	return err
}

// Unerased lists deleted artifacts that still hold something Erase removes:
// pages deleted before erasure existed, and guest pages the GC expired.
func (s *sqlStore) Unerased(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM artifacts a WHERE a.deleted_at IS NOT NULL AND (a.title<>'' OR a.project IS NOT NULL OR a.tags IS NOT NULL OR a.cover_path IS NOT NULL OR a.share_code<>''
 OR EXISTS(SELECT 1 FROM versions v WHERE v.artifact_id=a.id) OR EXISTS(SELECT 1 FROM threads t WHERE t.artifact_id=a.id) OR EXISTS(SELECT 1 FROM artifact_views w WHERE w.artifact_id=a.id)
 OR EXISTS(SELECT 1 FROM artifact_view_hours w WHERE w.artifact_id=a.id) OR EXISTS(SELECT 1 FROM artifact_view_facets w WHERE w.artifact_id=a.id)
 OR EXISTS(SELECT 1 FROM page_state p WHERE p.artifact_id=a.id)) LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetExpiry sets when an artifact stops being served, or clears it. Like the
// other access settings it leaves the content timestamp alone.
func (s *sqlStore) SetExpiry(ctx context.Context, id string, at *time.Time) error {
	at = utc(at)
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET expires_at=? WHERE id=? AND deleted_at IS NULL`), at, id)
	return affected(res, err)
}

// utc stores times in UTC: SQLite compares timestamps as text, so a value
// written with another offset would sort wrongly against now.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
