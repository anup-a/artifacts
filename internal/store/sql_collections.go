package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// deckSize is how many of a collection's newest artifacts a summary carries:
// the three cards of its stacked deck.
const deckSize = 3

// Collections lists every project label of a tenant's live artifacts as a
// collection, most recently active first. Each summary carries the saved
// metadata (empty when none) and its newest artifacts for the deck. The
// newest artifacts are read through ListArtifacts, one query per label, so
// timestamps keep their column type in both dialects instead of passing
// through an aggregate.
func (s *sqlStore) Collections(ctx context.Context, tenantID string) ([]CollectionSummary, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT a.project, COUNT(*), COALESCE(c.description,''), COALESCE(c.positions,''), COALESCE(c.share_slug,'')
		FROM artifacts a LEFT JOIN collections c ON c.tenant_id=a.tenant_id AND c.name=a.project
		WHERE a.tenant_id=? AND a.deleted_at IS NULL AND a.project IS NOT NULL
		GROUP BY a.project, c.description, c.positions, c.share_slug`), tenantID)
	if err != nil {
		return nil, err
	}
	out := []CollectionSummary{}
	for rows.Next() {
		var c CollectionSummary
		var positions string
		if err := rows.Scan(&c.Name, &c.Count, &c.Description, &positions, &c.ShareSlug); err != nil {
			rows.Close()
			return nil, err
		}
		c.TenantID = tenantID
		if c.Positions, err = decodePositions(positions); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		latest, err := s.ListArtifacts(ctx, tenantID, ArtifactFilter{Project: out[i].Name, Limit: deckSize})
		if err != nil {
			return nil, err
		}
		out[i].Latest = latest
		if len(latest) > 0 {
			out[i].ActiveAt = latest[0].UpdatedAt
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].ActiveAt.Equal(out[b].ActiveAt) {
			return out[a].ActiveAt.After(out[b].ActiveAt)
		}
		return out[a].Name < out[b].Name
	})
	return out, nil
}

const collectionCols = `tenant_id,name,description,positions,COALESCE(share_slug,''),updated_at`

func scanCollection(row interface{ Scan(...any) error }) (Collection, error) {
	var c Collection
	var positions string
	if err := row.Scan(&c.TenantID, &c.Name, &c.Description, &positions, &c.ShareSlug, &c.UpdatedAt); err != nil {
		return c, notFound(err)
	}
	var err error
	c.Positions, err = decodePositions(positions)
	return c, err
}

// Collection returns the saved metadata of one collection, or ErrNotFound when
// nothing has been saved for that label yet.
func (s *sqlStore) Collection(ctx context.Context, tenantID, name string) (Collection, error) {
	return scanCollection(s.db.QueryRowContext(ctx, s.q(`SELECT `+collectionCols+` FROM collections WHERE tenant_id=? AND name=?`), tenantID, name))
}

// CollectionByShareSlug resolves a public collection link. An empty slug
// never matches: unshared rows store NULL, and "" is not a link.
func (s *sqlStore) CollectionByShareSlug(ctx context.Context, slug string) (Collection, error) {
	if slug == "" {
		return Collection{}, ErrNotFound
	}
	return scanCollection(s.db.QueryRowContext(ctx, s.q(`SELECT `+collectionCols+` FROM collections WHERE share_slug=?`), slug))
}

// SaveCollection writes a collection's metadata, creating the row on first save.
func (s *sqlStore) SaveCollection(ctx context.Context, c Collection, at time.Time) error {
	positions, err := encodePositions(c.Positions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.q(`INSERT INTO collections(tenant_id,name,description,positions,share_slug,updated_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT (tenant_id,name) DO UPDATE SET description=excluded.description, positions=excluded.positions, share_slug=excluded.share_slug, updated_at=excluded.updated_at`),
		c.TenantID, c.Name, c.Description, positions, nullIfEmpty(c.ShareSlug), at)
	return err
}

// RenameCollection relabels every artifact of a tenant from one project label
// to another, deleted ones included so the label never splits, and moves the
// saved metadata with it. Renaming onto a label that already exists merges the
// two: the target keeps its own description unless it has none, the source's
// order is appended after the target's, and the source's share link is dropped
// so a merge can never publish the target's artifacts through it. It returns
// how many live artifacts moved.
func (s *sqlStore) RenameCollection(ctx context.Context, tenantID, from, to string, at time.Time) (int, error) {
	if from == to {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var moved int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM artifacts WHERE tenant_id=? AND project=? AND deleted_at IS NULL`), tenantID, from).Scan(&moved); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE artifacts SET project=? WHERE tenant_id=? AND project=?`), to, tenantID, from); err != nil {
		return 0, err
	}
	source, err := scanCollection(tx.QueryRowContext(ctx, s.q(`SELECT `+collectionCols+` FROM collections WHERE tenant_id=? AND name=?`), tenantID, from))
	if errors.Is(err, ErrNotFound) {
		return moved, tx.Commit()
	}
	if err != nil {
		return 0, err
	}
	target, err := scanCollection(tx.QueryRowContext(ctx, s.q(`SELECT `+collectionCols+` FROM collections WHERE tenant_id=? AND name=?`), tenantID, to))
	switch {
	case errors.Is(err, ErrNotFound):
		_, err = tx.ExecContext(ctx, s.q(`UPDATE collections SET name=?, updated_at=? WHERE tenant_id=? AND name=?`), to, at, tenantID, from)
	case err == nil:
		if target.Description == "" {
			target.Description = source.Description
		}
		positions, perr := encodePositions(append(target.Positions, source.Positions...))
		if perr != nil {
			return 0, perr
		}
		if _, err = tx.ExecContext(ctx, s.q(`UPDATE collections SET description=?, positions=?, updated_at=? WHERE tenant_id=? AND name=?`), target.Description, positions, at, tenantID, to); err == nil {
			_, err = tx.ExecContext(ctx, s.q(`DELETE FROM collections WHERE tenant_id=? AND name=?`), tenantID, from)
		}
	}
	if err != nil {
		return 0, err
	}
	return moved, tx.Commit()
}

// encodePositions stores an empty order as "" so "publish order" has one form.
func encodePositions(ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

func decodePositions(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func nullIfEmpty(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
