package store

import (
	"context"
	"errors"
	"sort"
	"time"
)

// ErrStateFull is returned by PatchState when the write would take the page
// past StateLimits: nothing of the patch is applied.
var ErrStateFull = errors.New("page state is full")

// StateLimits caps one page's state as a whole. Per-key and per-patch rules
// (key syntax, value size, keys per patch) are the caller's to check before
// a patch reaches the store.
type StateLimits struct {
	Keys  int
	Bytes int64
}

// PageState is a page's whole state: every key with its value as JSON text,
// the revision (how many writes it has seen), and its size in keys and bytes
// (key plus value, as stored).
type PageState struct {
	Items    map[string]string
	Revision int64
	Keys     int
	Bytes    int64
}

// StateStore keeps page state: a small key-value store per artifact that the
// page's own script reads and writes. It belongs to the artifact, not to a
// version, and a write never makes a version.
type StateStore interface {
	// State reads an artifact's whole state. An artifact with none has an
	// empty, non-nil Items. ErrNotFound when the artifact does not exist.
	State(ctx context.Context, artifactID string) (PageState, error)
	// StateRevision is the artifact's current revision alone, for a reader
	// asking whether anything changed.
	StateRevision(ctx context.Context, artifactID string) (int64, error)
	// PatchState sets the keys in set (values are JSON text) and deletes
	// those in del, as one write, and returns the new revision. A write that
	// would leave the page over lim fails with ErrStateFull and changes
	// nothing.
	PatchState(ctx context.Context, artifactID string, set map[string]string, del []string, lim StateLimits, at time.Time) (int64, error)
	// ClearState deletes every key and returns the new revision.
	ClearState(ctx context.Context, artifactID string) (int64, error)
}

func (s *sqlStore) SetStateEnabled(ctx context.Context, id string, on bool) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET state_enabled=? WHERE id=?`), on, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// octets is the dialect's byte length of a text column: SQLite's LENGTH
// counts characters unless it is given a blob.
func (s *sqlStore) octets(col string) string {
	if s.dialect == "postgres" {
		return "OCTET_LENGTH(" + col + ")"
	}
	return "LENGTH(CAST(" + col + " AS BLOB))"
}

func (s *sqlStore) State(ctx context.Context, artifactID string) (PageState, error) {
	st := PageState{Items: map[string]string{}}
	rev, err := s.StateRevision(ctx, artifactID)
	if err != nil {
		return st, err
	}
	st.Revision = rev
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT name,value FROM page_state WHERE artifact_id=? ORDER BY name`), artifactID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		st.Items[k] = v
		st.Keys++
		st.Bytes += int64(len(k) + len(v))
	}
	return st, rows.Err()
}

func (s *sqlStore) StateRevision(ctx context.Context, artifactID string) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT state_revision FROM artifacts WHERE id=?`), artifactID).Scan(&rev)
	return rev, notFound(err)
}

func (s *sqlStore) PatchState(ctx context.Context, artifactID string, set map[string]string, del []string, lim StateLimits, at time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// The revision moves first: on Postgres the row lock it takes orders
	// concurrent writes to one page, so the size check below sees a settled
	// total. SQLite runs one writer at a time anyway.
	res, err := tx.ExecContext(ctx, s.q(`UPDATE artifacts SET state_revision=state_revision+1 WHERE id=?`), artifactID)
	if err := affected(res, err); err != nil {
		return 0, err
	}
	for _, k := range del {
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM page_state WHERE artifact_id=? AND name=?`), artifactID, k); err != nil {
			return 0, err
		}
	}
	// Sorted, so two writers touching the same keys take their locks in
	// the same order.
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO page_state(artifact_id,name,value,updated_at) VALUES (?,?,?,?)
ON CONFLICT(artifact_id,name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`), artifactID, k, set[k], at.UTC()); err != nil {
			return 0, err
		}
	}
	if len(set) > 0 {
		var n int
		var bytes int64
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*), COALESCE(SUM(`+s.octets("name")+`+`+s.octets("value")+`),0) FROM page_state WHERE artifact_id=?`), artifactID).Scan(&n, &bytes); err != nil {
			return 0, err
		}
		if (lim.Keys > 0 && n > lim.Keys) || (lim.Bytes > 0 && bytes > lim.Bytes) {
			return 0, ErrStateFull
		}
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, s.q(`SELECT state_revision FROM artifacts WHERE id=?`), artifactID).Scan(&rev); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *sqlStore) ClearState(ctx context.Context, artifactID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, s.q(`UPDATE artifacts SET state_revision=state_revision+1 WHERE id=?`), artifactID)
	if err := affected(res, err); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM page_state WHERE artifact_id=?`), artifactID); err != nil {
		return 0, err
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, s.q(`SELECT state_revision FROM artifacts WHERE id=?`), artifactID).Scan(&rev); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}
