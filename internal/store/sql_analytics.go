package store

import (
	"context"
	"database/sql"
	"strings"
)

// AddViewDetails commits a flush's hour and facet rows together, so a failed
// batch can be retried without counting any row twice.
func (s *sqlStore) AddViewDetails(ctx context.Context, hours []ViewHour, facets []ViewFacet) error {
	if len(hours) == 0 && len(facets) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hours {
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO artifact_view_hours(artifact_id,hour,views,visitors) SELECT ?,?,?,? WHERE `+notDeleted+`
			ON CONFLICT (artifact_id,hour) DO UPDATE SET views=artifact_view_hours.views+excluded.views, visitors=artifact_view_hours.visitors+excluded.visitors`),
			h.ArtifactID, h.Hour, h.Views, h.Visitors, h.ArtifactID); err != nil {
			return err
		}
	}
	for _, f := range facets {
		if err := s.addFacet(ctx, tx, f); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// addFacet adds to an existing value's row, or starts one while the page has
// fewer than FacetCap values that day for the kind; past that the views go to
// FacetOther. The cap is what bounds the table against made-up referrers.
func (s *sqlStore) addFacet(ctx context.Context, tx *sql.Tx, f ViewFacet) error {
	res, err := tx.ExecContext(ctx, s.q(`UPDATE artifact_view_facets SET views=views+? WHERE artifact_id=? AND day=? AND kind=? AND value=?`),
		f.Views, f.ArtifactID, f.Day, f.Kind, f.Value)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	value := f.Value
	if value != FacetOther {
		var n int
		if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM artifact_view_facets WHERE artifact_id=? AND day=? AND kind=? AND value<>?`),
			f.ArtifactID, f.Day, f.Kind, FacetOther).Scan(&n); err != nil {
			return err
		}
		if n >= FacetCap {
			value = FacetOther
		}
	}
	_, err = tx.ExecContext(ctx, s.q(`INSERT INTO artifact_view_facets(artifact_id,day,kind,value,views) SELECT ?,?,?,?,? WHERE `+notDeleted+`
		ON CONFLICT (artifact_id,day,kind,value) DO UPDATE SET views=artifact_view_facets.views+excluded.views`),
		f.ArtifactID, f.Day, f.Kind, value, f.Views, f.ArtifactID)
	return err
}

func (s *sqlStore) PruneViewHours(ctx context.Context, before string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM artifact_view_hours WHERE hour<?`), before)
	return err
}

// analyticsScope limits a query aliased v to one page of the tenant, or to
// every live page of it.
func analyticsScope(q AnalyticsQuery) (string, []any) {
	if q.ArtifactID != "" {
		return `v.artifact_id=? AND v.artifact_id IN (SELECT id FROM artifacts WHERE tenant_id=?)`, []any{q.ArtifactID, q.TenantID}
	}
	return `v.artifact_id IN (SELECT id FROM artifacts WHERE tenant_id=? AND deleted_at IS NULL)`, []any{q.TenantID}
}

func (s *sqlStore) Analytics(ctx context.Context, q AnalyticsQuery) (Analytics, error) {
	var out Analytics
	scope, args := analyticsScope(q)
	with := func(extra ...any) []any { return append(append([]any{}, args...), extra...) }
	if err := s.db.QueryRowContext(ctx, s.q(`SELECT COALESCE(SUM(v.count),0), COALESCE(MIN(v.day),'') FROM artifact_views v WHERE `+scope), args...).
		Scan(&out.AllTimeViews, &out.FirstDay); err != nil {
		return out, err
	}
	series := `SELECT v.day, SUM(v.count), SUM(v.visitors) FROM artifact_views v WHERE ` + scope + ` AND v.day>=? AND v.day<? GROUP BY v.day ORDER BY v.day`
	from, to := q.FromDay, q.ToDay
	if q.Hourly {
		series = `SELECT v.hour, SUM(v.views), SUM(v.visitors) FROM artifact_view_hours v WHERE ` + scope + ` AND v.hour>=? AND v.hour<? GROUP BY v.hour ORDER BY v.hour`
		from, to = q.FromHour, q.ToHour
	}
	rows, err := s.db.QueryContext(ctx, s.q(series), with(from, to)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p AnalyticsPoint
		if err := rows.Scan(&p.Bucket, &p.Views, &p.Visitors); err != nil {
			rows.Close()
			return out, err
		}
		out.Views += p.Views
		out.Visitors += p.Visitors
		out.Series = append(out.Series, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	top := q.Top
	if top <= 0 {
		top = 10
	}
	for _, f := range []struct {
		kind string
		dst  *[]FacetCount
	}{{FacetReferrer, &out.Referrers}, {FacetCountry, &out.Countries}, {FacetDevice, &out.Devices}, {FacetPath, &out.Paths}} {
		// Paths only mean something within one page, so a workspace report
		// keeps them apart per page.
		sel, group := `''`, `v.value`
		if f.kind == FacetPath {
			sel, group = `v.artifact_id`, `v.artifact_id, v.value`
		}
		list, err := s.facetCounts(ctx, `SELECT `+sel+`, v.value, SUM(v.views) AS n FROM artifact_view_facets v WHERE `+scope+
			` AND v.kind=? AND v.day>=? AND v.day<? GROUP BY `+group+` ORDER BY n DESC, v.value LIMIT ?`, with(f.kind, q.FromDay, q.ToDay, top))
		if err != nil {
			return out, err
		}
		*f.dst = list
	}
	if q.ArtifactID == "" {
		pages := `SELECT a.id, a.title, a.visibility, COALESCE(a.slug,''), SUM(v.count) AS n, SUM(v.visitors) FROM artifact_views v JOIN artifacts a ON a.id=v.artifact_id
			WHERE a.tenant_id=? AND a.deleted_at IS NULL AND v.day>=? AND v.day<? GROUP BY a.id, a.title, a.visibility, a.slug ORDER BY n DESC, a.id LIMIT ?`
		if q.Hourly {
			pages = `SELECT a.id, a.title, a.visibility, COALESCE(a.slug,''), SUM(v.views) AS n, SUM(v.visitors) FROM artifact_view_hours v JOIN artifacts a ON a.id=v.artifact_id
			WHERE a.tenant_id=? AND a.deleted_at IS NULL AND v.hour>=? AND v.hour<? GROUP BY a.id, a.title, a.visibility, a.slug ORDER BY n DESC, a.id LIMIT ?`
		}
		rows, err := s.db.QueryContext(ctx, s.q(pages), q.TenantID, from, to, top)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for rows.Next() {
			var p PageViews
			if err := rows.Scan(&p.ArtifactID, &p.Title, &p.Visibility, &p.Slug, &p.Views, &p.Visitors); err != nil {
				return out, err
			}
			out.Pages = append(out.Pages, p)
		}
		return out, rows.Err()
	}
	return out, nil
}

func (s *sqlStore) facetCounts(ctx context.Context, query string, args []any) ([]FacetCount, error) {
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FacetCount
	for rows.Next() {
		var f FacetCount
		if err := rows.Scan(&f.ArtifactID, &f.Value, &f.Views); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *sqlStore) ViewTotals(ctx context.Context, ids []string, fromDay string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{fromDay}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT artifact_id, SUM(count) FROM artifact_views WHERE day>=? AND artifact_id IN (?`+strings.Repeat(",?", len(ids)-1)+`) GROUP BY artifact_id`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
