package app

import (
	"context"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/store"
)

func TestViewsCardAndListCounts(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("viewsviews01", "Launch plan", "")
	e.version(a.ID, 1, 100)
	ctx := context.Background()
	today := e.clock.t.Format("2006-01-02")
	if err := e.st.AddViews(ctx, []store.ViewCount{{ArtifactID: a.ID, Day: today, Count: 1234, Visitors: 321}, {ArtifactID: a.ID, Day: "2026-01-01", Count: 5}}); err != nil {
		t.Fatal(err)
	}
	// Facet values come from visitors; whatever got stored is shown as text.
	if err := e.st.AddViewDetails(ctx, []store.ViewHour{{ArtifactID: a.ID, Hour: e.clock.t.Format("2006-01-02T15"), Views: 1234, Visitors: 321}}, []store.ViewFacet{
		{ArtifactID: a.ID, Day: today, Kind: store.FacetReferrer, Value: "news.ycombinator.com", Views: 900},
		{ArtifactID: a.ID, Day: today, Kind: store.FacetReferrer, Value: `<img src=x onerror=alert(1)>`, Views: 1},
		{ArtifactID: a.ID, Day: today, Kind: store.FacetCountry, Value: "DE", Views: 1234},
	}); err != nil {
		t.Fatal(err)
	}

	res := e.get("/app/artifacts/" + a.ID)
	want(t, res, 200, `id="views"`, ">1,234<", ">321<", "1,239 all time", `class="views-chart"`, "news.ycombinator.com", ">DE<",
		`href="/app/artifacts/viewsviews01?range=30d#views"`, `class="is-on" aria-current="true">7d<`,
		`src="http://share.test/a/viewsviews01/?byagent_preview=1"`)
	if strings.Contains(res.Body, "<img src=x") {
		t.Fatal("referrer rendered as markup")
	}
	if n := bars(res.Body); n != 7 {
		t.Fatalf("7d chart has %d bars", n)
	}

	res = e.get("/app/artifacts/" + a.ID + "?range=24h")
	if n := bars(res.Body); n != 24 || !strings.Contains(res.Body, "1,234 views, 321 visitors") {
		t.Fatalf("24h chart: %d bars", n)
	}
	// A made-up range falls back to the default rather than failing the page.
	want(t, e.get("/app/artifacts/"+a.ID+"?range=nope"), 200, `class="is-on" aria-current="true">7d<`)

	empty := e.publish("viewsviews02", "Quiet page", "")
	e.version(empty.ID, 1, 100)
	want(t, e.get("/app/artifacts/"+empty.ID), 200, "No views in this range")

	// The list shows the 30 day count next to each page.
	want(t, e.get("/app/artifacts"), 200, `title="1,234 views in the last 30 days"`)
}

// bars counts the bars in the Views chart.
func bars(body string) int {
	_, chart, _ := strings.Cut(body, `class="views-chart"`)
	chart, _, _ = strings.Cut(chart, "</svg>")
	return strings.Count(chart, "<rect ")
}
