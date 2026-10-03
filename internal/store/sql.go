package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type sqlStore struct {
	db      *sql.DB
	dialect string
}

// q rewrites ? placeholders to $1.. for postgres.
func (s *sqlStore) q(query string) string {
	if s.dialect != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *sqlStore) Migrate(ctx context.Context) error {
	if err := runMigrations(ctx, s.db, s.q); err != nil {
		return err
	}
	return s.claimOrphanAdmin(ctx)
}
func (s *sqlStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *sqlStore) Close() error                   { return s.db.Close() }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *sqlStore) UpdateTitle(ctx context.Context, id, title string) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET title=?, updated_at=? WHERE id=?`), title, time.Now().UTC(), id)
	return err
}

// SetCommentsEnabled flips the viewer comment switch. It does not touch
// updated_at: nothing about the published content changed.
func (s *sqlStore) SetCommentsEnabled(ctx context.Context, id string, on bool) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET comments_enabled=? WHERE id=?`), on, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqlStore) SoftDelete(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET deleted_at=?, updated_at=? WHERE id=? AND deleted_at IS NULL`), at, at, id)
	return err
}

func (s *sqlStore) AddVersion(ctx context.Context, v Version, files []File) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO versions(artifact_id,version,created_at,bytes,file_count,agent,model,key_name) VALUES (?,?,?,?,?,?,?,?)`),
		v.ArtifactID, v.Version, v.CreatedAt, v.Bytes, v.FileCount, v.Agent, v.Model, v.KeyName); err != nil {
		return err
	}
	for _, f := range files {
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO files(artifact_id,version,path,sha256,size,content_type) VALUES (?,?,?,?,?,?)`),
			v.ArtifactID, v.Version, f.Path, f.SHA256, f.Size, f.ContentType); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, s.q(`UPDATE blobs SET refcount=refcount+1 WHERE sha256=?`), f.SHA256)
		if err != nil {
			return err
		}
		// No blobs row means the increment hit nothing: the version would
		// reference bytes with no refcount keeping them alive, so GC could
		// reclaim them out from under it. Fail the whole transaction.
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("%w: %s", ErrBlobMissing, f.SHA256)
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE artifacts SET current_version=?, updated_at=? WHERE id=?`), v.Version, v.CreatedAt, v.ArtifactID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) Versions(ctx context.Context, artifactID string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT artifact_id,version,created_at,bytes,file_count,agent,model,key_name FROM versions WHERE artifact_id=? ORDER BY version`), artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ArtifactID, &v.Version, &v.CreatedAt, &v.Bytes, &v.FileCount, &v.Agent, &v.Model, &v.KeyName); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqlStore) Files(ctx context.Context, artifactID string, version int) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT path,sha256,size,content_type FROM files WHERE artifact_id=? AND version=? ORDER BY path`), artifactID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.SHA256, &f.Size, &f.ContentType); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *sqlStore) PruneVersions(ctx context.Context, artifactID string, keep int) ([]int, error) {
	vs, err := s.Versions(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if len(vs) <= keep {
		return nil, nil
	}
	protected := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT DISTINCT version FROM threads WHERE artifact_id=? AND status='open'`), artifactID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		protected[v] = true
	}
	rows.Close()
	// Only the oldest (len(vs)-keep) versions are eligible for pruning; a
	// protected one among them is skipped in place (not replaced by a newer
	// version), so it stays retained beyond the requested keep count.
	excess := len(vs) - keep
	candidates := vs[:excess]
	var pruned []int
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, v := range candidates {
		if protected[v.Version] {
			continue
		}
		// Decrement per file row (mirroring the per-row increment in AddVersion), not
		// per distinct sha, so a version with two files sharing one sha (same content,
		// different paths) doesn't leak a refcount.
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE blobs SET refcount = refcount - (SELECT COUNT(*) FROM files f WHERE f.artifact_id=? AND f.version=? AND f.sha256=blobs.sha256) WHERE sha256 IN (SELECT sha256 FROM files WHERE artifact_id=? AND version=?)`), artifactID, v.Version, artifactID, v.Version); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM files WHERE artifact_id=? AND version=?`), artifactID, v.Version); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM versions WHERE artifact_id=? AND version=?`), artifactID, v.Version); err != nil {
			return nil, err
		}
		pruned = append(pruned, v.Version)
	}
	return pruned, tx.Commit()
}

func (s *sqlStore) UpsertBlob(ctx context.Context, sha string, size int64) error {
	var q string
	if s.dialect == "postgres" {
		q = `INSERT INTO blobs(sha256,size,refcount,created_at) VALUES (?,?,0,?) ON CONFLICT (sha256) DO NOTHING`
	} else {
		q = `INSERT OR IGNORE INTO blobs(sha256,size,refcount,created_at) VALUES (?,?,0,?)`
	}
	_, err := s.db.ExecContext(ctx, s.q(q), sha, size, time.Now().UTC())
	return err
}

func (s *sqlStore) HasBlobs(ctx context.Context, shas []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(shas) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(shas)), ",")
	args := make([]any, len(shas))
	for i, v := range shas {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT sha256 FROM blobs WHERE sha256 IN (`+ph+`)`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out[sha] = true
	}
	return out, rows.Err()
}

func (s *sqlStore) ZeroRefBlobs(ctx context.Context, olderThan time.Time) ([]string, error) {
	// The NOT EXISTS clause is a belt-and-braces guard against refcount drift:
	// a blob is collectable only if no files row still points at it.
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT sha256 FROM blobs WHERE refcount<=0 AND created_at<? AND NOT EXISTS (SELECT 1 FROM files f WHERE f.sha256 = blobs.sha256)`), olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out = append(out, sha)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteBlob(ctx context.Context, sha string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM blobs WHERE sha256=? AND refcount<=0`), sha)
	return err
}

func (s *sqlStore) CreateThread(ctx context.Context, t Thread, first Comment) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO threads(id,artifact_id,version,anchor_json,status,anchor_status,created_at) VALUES (?,?,?,?,?,?,?)`),
		t.ID, t.ArtifactID, t.Version, t.AnchorJSON, t.Status, t.AnchorStatus, t.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO comments(id,thread_id,author_name,author_kind,body,created_at) VALUES (?,?,?,?,?,?)`),
		first.ID, first.ThreadID, first.AuthorName, first.AuthorKind, first.Body, first.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

const threadCols = `id,artifact_id,version,anchor_json,status,anchor_status,created_at,resolved_at,progress`

func scanThread(row interface{ Scan(...any) error }) (Thread, error) {
	var t Thread
	err := row.Scan(&t.ID, &t.ArtifactID, &t.Version, &t.AnchorJSON, &t.Status, &t.AnchorStatus, &t.CreatedAt, &t.ResolvedAt, &t.Progress)
	return t, notFound(err)
}

func (s *sqlStore) Thread(ctx context.Context, id string) (Thread, error) {
	return scanThread(s.db.QueryRowContext(ctx, s.q(`SELECT `+threadCols+` FROM threads WHERE id=?`), id))
}

func (s *sqlStore) Threads(ctx context.Context, artifactID, status string, since time.Time, version int) ([]Thread, error) {
	q := `SELECT ` + threadCols + ` FROM threads WHERE artifact_id=? AND created_at>=?`
	args := []any{artifactID, since}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	if version > 0 {
		q += ` AND version=?`
		args = append(args, version)
	}
	q += ` ORDER BY created_at`
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *sqlStore) Comments(ctx context.Context, threadIDs []string) (map[string][]Comment, error) {
	out := map[string][]Comment{}
	if len(threadIDs) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(threadIDs)), ",")
	args := make([]any, len(threadIDs))
	for i, v := range threadIDs {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,thread_id,author_name,author_kind,body,created_at FROM comments WHERE thread_id IN (`+ph+`) ORDER BY created_at`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.ThreadID, &c.AuthorName, &c.AuthorKind, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out[c.ThreadID] = append(out[c.ThreadID], c)
	}
	return out, rows.Err()
}

func (s *sqlStore) AddComment(ctx context.Context, c Comment) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO comments(id,thread_id,author_name,author_kind,body,created_at) VALUES (?,?,?,?,?,?)`),
		c.ID, c.ThreadID, c.AuthorName, c.AuthorKind, c.Body, c.CreatedAt)
	return err
}

func (s *sqlStore) ResolveThread(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE threads SET status='resolved', resolved_at=? WHERE id=?`), at, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveAllThreads closes every open thread of an artifact. Used on delete:
// open threads pin their version against pruning, and once the artifact is
// gone (410) nobody can resolve them, so the blobs would be unreclaimable.
func (s *sqlStore) ResolveAllThreads(ctx context.Context, artifactID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE threads SET status='resolved', resolved_at=? WHERE artifact_id=? AND status='open'`), at, artifactID)
	return err
}

func (s *sqlStore) SetAnchorStatus(ctx context.Context, threadID, status string) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE threads SET anchor_status=? WHERE id=?`), status, threadID)
	return err
}

func (s *sqlStore) CountOpenThreads(ctx context.Context, artifactID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM threads WHERE artifact_id=? AND status='open'`), artifactID).Scan(&n)
	return n, err
}

func (s *sqlStore) CountThreadsByVersion(ctx context.Context, artifactID string) (map[int]int, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT version, COUNT(*) FROM threads WHERE artifact_id=? GROUP BY version`), artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var v, n int
		if err := rows.Scan(&v, &n); err != nil {
			return nil, err
		}
		out[v] = n
	}
	return out, rows.Err()
}

// SetThreadProgress stores what the publisher is doing about a thread. It
// does not touch status: resolving is a separate call.
func (s *sqlStore) SetThreadProgress(ctx context.Context, id, progress string) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE threads SET progress=? WHERE id=?`), progress, id)
	return affected(res, err)
}
