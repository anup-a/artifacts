package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAnalyticsReports(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "an-main", "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateKey(ctx, "an-other", "other@example.test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	usageArtifact(t, s, k, "page-a", now)
	usageArtifact(t, s, k, "page-b", now)
	usageArtifact(t, s, other, "page-x", now)
	if err := s.AddViews(ctx, []ViewCount{
		{ArtifactID: "page-a", Day: "2026-09-01", Count: 50, Visitors: 40},
		{ArtifactID: "page-a", Day: "2026-10-03", Count: 5, Visitors: 4},
		{ArtifactID: "page-a", Day: "2026-10-04", Count: 7, Visitors: 3},
		{ArtifactID: "page-b", Day: "2026-10-04", Count: 2, Visitors: 2},
		{ArtifactID: "page-x", Day: "2026-10-04", Count: 1000, Visitors: 1000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddViewDetails(ctx, []ViewHour{
		{ArtifactID: "page-a", Hour: "2026-10-04T09", Views: 6, Visitors: 2},
		{ArtifactID: "page-a", Hour: "2026-10-04T10", Views: 1, Visitors: 1},
		{ArtifactID: "page-b", Hour: "2026-10-04T10", Views: 2, Visitors: 2},
		{ArtifactID: "page-a", Hour: "2026-10-01T10", Views: 9, Visitors: 9},
	}, []ViewFacet{
		{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetReferrer, Value: "news.ycombinator.com", Views: 4},
		{ArtifactID: "page-a", Day: "2026-10-03", Kind: FacetReferrer, Value: "news.ycombinator.com", Views: 1},
		{ArtifactID: "page-b", Day: "2026-10-04", Kind: FacetReferrer, Value: "news.ycombinator.com", Views: 2},
		{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetReferrer, Value: "(direct)", Views: 3},
		{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetCountry, Value: "DE", Views: 7},
		{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetPath, Value: "/", Views: 6},
		{ArtifactID: "page-b", Day: "2026-10-04", Kind: FacetPath, Value: "/", Views: 2},
		{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetPath, Value: "/b.html", Views: 1},
		{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetDevice, Value: "mobile", Views: 7},
		{ArtifactID: "page-x", Day: "2026-10-04", Kind: FacetReferrer, Value: "secret.example", Views: 1000},
	}); err != nil {
		t.Fatal(err)
	}

	week := AnalyticsQuery{TenantID: k.TenantID, ArtifactID: "page-a", FromDay: "2026-09-28", ToDay: "2026-10-05"}
	a, err := s.Analytics(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if a.AllTimeViews != 62 || a.FirstDay != "2026-09-01" || a.Views != 12 || a.Visitors != 7 || len(a.Series) != 2 || len(a.Pages) != 0 {
		t.Fatalf("page week: %+v", a)
	}
	if len(a.Referrers) != 2 || a.Referrers[0].Value != "news.ycombinator.com" || a.Referrers[0].Views != 5 {
		t.Fatalf("referrers %+v", a.Referrers)
	}
	if len(a.Paths) != 2 || a.Paths[0] != (FacetCount{ArtifactID: "page-a", Value: "/", Views: 6}) {
		t.Fatalf("paths %+v", a.Paths)
	}
	if len(a.Countries) != 1 || len(a.Devices) != 1 {
		t.Fatalf("countries %+v devices %+v", a.Countries, a.Devices)
	}

	// Another tenant's page reads as empty even by its id.
	stolen := week
	stolen.ArtifactID = "page-x"
	if a, err := s.Analytics(ctx, stolen); err != nil || a.AllTimeViews != 0 || len(a.Referrers) != 0 || len(a.Series) != 0 {
		t.Fatalf("cross-tenant read: %+v %v", a, err)
	}

	hourly := AnalyticsQuery{TenantID: k.TenantID, Hourly: true, FromHour: "2026-10-03T11", ToHour: "2026-10-04T11", FromDay: "2026-10-03", ToDay: "2026-10-05", Top: 1}
	a, err = s.Analytics(ctx, hourly)
	if err != nil {
		t.Fatal(err)
	}
	if a.Views != 9 || a.Visitors != 5 || len(a.Series) != 2 || a.Series[1] != (AnalyticsPoint{Bucket: "2026-10-04T10", Views: 3, Visitors: 3}) {
		t.Fatalf("account 24h: %+v", a)
	}
	if len(a.Pages) != 1 || a.Pages[0].ArtifactID != "page-a" || a.Pages[0].Views != 7 {
		t.Fatalf("top pages %+v", a.Pages)
	}
	if len(a.Referrers) != 1 || a.Referrers[0].Views != 7 || a.Referrers[0].Value != "news.ycombinator.com" {
		t.Fatalf("account referrers %+v", a.Referrers)
	}
	if a.AllTimeViews != 64 {
		t.Fatalf("account all time %d", a.AllTimeViews)
	}

	totals, err := s.ViewTotals(ctx, []string{"page-a", "page-b", "page-none"}, "2026-10-04")
	if err != nil || totals["page-a"] != 7 || totals["page-b"] != 2 || len(totals) != 2 {
		t.Fatalf("totals %v %v", totals, err)
	}

	if err := s.PruneViewHours(ctx, "2026-10-02T00"); err != nil {
		t.Fatal(err)
	}
	all := AnalyticsQuery{TenantID: k.TenantID, Hourly: true, FromHour: "", ToHour: "9999"}
	if a, err := s.Analytics(ctx, all); err != nil || a.Views != 9 {
		t.Fatalf("after prune: %+v %v", a, err)
	}

	// Delete erases every analytics row, and a late flush for the deleted
	// page writes nothing back.
	if err := s.SoftDelete(ctx, "page-a", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, "page-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddViews(ctx, []ViewCount{{ArtifactID: "page-a", Day: "2026-10-04", Count: 1, Visitors: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddViewDetails(ctx, []ViewHour{{ArtifactID: "page-a", Hour: "2026-10-04T11", Views: 1}},
		[]ViewFacet{{ArtifactID: "page-a", Day: "2026-10-04", Kind: FacetReferrer, Value: "late.test", Views: 1}}); err != nil {
		t.Fatal(err)
	}
	db := s.(*sqlStore)
	for _, table := range []string{"artifact_views", "artifact_view_hours", "artifact_view_facets"} {
		var n int
		if err := db.db.QueryRowContext(ctx, db.q(`SELECT COUNT(*) FROM `+table+` WHERE artifact_id=?`), "page-a").Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows left (%v)", table, n, err)
		}
	}
	if left, err := s.Unerased(ctx); err != nil || len(left) != 0 {
		t.Fatalf("unerased %v %v", left, err)
	}
}

// A page gets at most FacetCap values per day and kind; the rest of the
// views land in FacetOther, across flushes too.
func TestFacetCap(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "cap", "cap@example.test")
	if err != nil {
		t.Fatal(err)
	}
	usageArtifact(t, s, k, "capped", time.Now().UTC())
	for batch := 0; batch < 2; batch++ {
		var fs []ViewFacet
		for i := 0; i < FacetCap; i++ {
			fs = append(fs, ViewFacet{ArtifactID: "capped", Day: "2026-10-04", Kind: FacetReferrer, Value: fmt.Sprintf("b%d-%d.test", batch, i), Views: 1})
		}
		// A value already kept still adds up after the cap is reached.
		fs = append(fs, ViewFacet{ArtifactID: "capped", Day: "2026-10-04", Kind: FacetReferrer, Value: "b0-0.test", Views: 1})
		if err := s.AddViewDetails(ctx, nil, fs); err != nil {
			t.Fatal(err)
		}
	}
	db := s.(*sqlStore)
	var rows int
	var other, kept int64
	if err := db.db.QueryRowContext(ctx, db.q(`SELECT COUNT(*) FROM artifact_view_facets WHERE artifact_id=?`), "capped").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, db.q(`SELECT views FROM artifact_view_facets WHERE artifact_id=? AND value=?`), "capped", FacetOther).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, db.q(`SELECT views FROM artifact_view_facets WHERE artifact_id=? AND value=?`), "capped", "b0-0.test").Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if rows != FacetCap+1 || other != FacetCap || kept != 3 {
		t.Fatalf("rows %d, other %d, kept %d", rows, other, kept)
	}
}
