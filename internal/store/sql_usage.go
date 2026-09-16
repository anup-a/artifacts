package store

import (
	"context"
	"time"
)

// AddViews commits an entire flush together, so a failed batch can be retried.
func (s *sqlStore) AddViews(ctx context.Context, views []ViewCount) error {
	if len(views) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range views {
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO artifact_views(artifact_id,day,count) VALUES (?,?,?)
			ON CONFLICT (artifact_id,day) DO UPDATE SET count=artifact_views.count+excluded.count`), v.ArtifactID, v.Day, v.Count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Usage counts events within the UTC month and live data at its cutoff.
// Historical totals can only include versions that have not been pruned.
func (s *sqlStore) Usage(ctx context.Context, tenantID string, month time.Time) (MonthUsage, error) {
	month = month.UTC()
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := month.AddDate(0, 1, 0)
	cutoff := end
	viewEnd := end.Format(time.DateOnly)
	if now := time.Now().UTC(); now.Before(cutoff) {
		cutoff = now
		// View buckets include today, even though the day has not ended yet.
		viewEnd = now.AddDate(0, 0, 1).Format(time.DateOnly)
	}
	u := MonthUsage{Month: month}
	err := s.db.QueryRowContext(ctx, s.q(`SELECT
		(SELECT COUNT(*) FROM versions v JOIN artifacts a ON a.id=v.artifact_id
		 WHERE a.tenant_id=? AND v.created_at>=? AND v.created_at<?),
		(SELECT COALESCE(SUM(v.bytes),0) FROM versions v JOIN artifacts a ON a.id=v.artifact_id
		 WHERE a.tenant_id=? AND v.created_at<? AND (a.deleted_at IS NULL OR a.deleted_at>=?)),
		(SELECT COALESCE(SUM(v.count),0) FROM artifact_views v JOIN artifacts a ON a.id=v.artifact_id
		 WHERE a.tenant_id=? AND v.day>=? AND v.day<?),
		(SELECT COUNT(*) FROM threads t JOIN artifacts a ON a.id=t.artifact_id
		 WHERE a.tenant_id=? AND t.created_at<? AND (t.status='open' OR t.resolved_at>=?)
		 AND (a.deleted_at IS NULL OR a.deleted_at>=?))`),
		tenantID, month, cutoff,
		tenantID, cutoff, cutoff,
		tenantID, month.Format(time.DateOnly), viewEnd,
		tenantID, cutoff, cutoff, cutoff,
	).Scan(&u.Publishes, &u.StorageBytes, &u.PageViews, &u.OpenComments)
	return u, err
}
