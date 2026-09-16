package store

import "context"

// SetMetadata replaces the gallery labels of one artifact. updated_at is left
// alone: relabelling is not a new version and must not reorder the list.
func (s *sqlStore) SetMetadata(ctx context.Context, id string, project *string, tags []string, coverPath *string, coverStyle string) error {
	encoded, err := encodeTags(tags)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET project=?, tags=?, cover_path=?, cover_style=? WHERE id=?`), project, encoded, coverPath, coverStyle, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Projects returns the distinct project labels among a tenant's live
// artifacts, most used first and then alphabetically so the chip order is
// stable between requests.
func (s *sqlStore) Projects(ctx context.Context, tenantID string) ([]ProjectCount, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT project, COUNT(*) FROM artifacts WHERE tenant_id=? AND deleted_at IS NULL AND project IS NOT NULL GROUP BY project ORDER BY COUNT(*) DESC, project ASC`), tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectCount{}
	for rows.Next() {
		var p ProjectCount
		if err := rows.Scan(&p.Name, &p.Count); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
