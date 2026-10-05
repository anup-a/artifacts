package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/internal/usage"
)

// analyticsTop is how long each ranked list in a report is.
const analyticsTop = 10

type analyticsTotals struct {
	AllTimeViews  int64 `json:"all_time_views"`
	RangeViews    int64 `json:"range_views"`
	RangeVisitors int64 `json:"range_visitors"`
}

type referrerCount struct {
	Referrer string `json:"referrer"`
	Views    int64  `json:"views"`
}

type countryCount struct {
	Country string `json:"country"`
	Views   int64  `json:"views"`
}

type deviceCount struct {
	Device string `json:"device"`
	Views  int64  `json:"views"`
}

type pathCount struct {
	ID    string `json:"id,omitempty"`
	Path  string `json:"path"`
	Views int64  `json:"views"`
}

type pageCount struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Views    int64  `json:"views"`
	Visitors int64  `json:"visitors"`
}

// analyticsReport is the shape of both reports. A page report carries id
// and url; the workspace report carries top_pages, and its top_paths name
// the page each path belongs to.
type analyticsReport struct {
	ID           string          `json:"id,omitempty"`
	URL          string          `json:"url,omitempty"`
	Range        string          `json:"range"`
	Bucket       string          `json:"bucket"`
	From         time.Time       `json:"from"`
	To           time.Time       `json:"to"`
	FirstViewDay *string         `json:"first_view_day"`
	Totals       analyticsTotals `json:"totals"`
	Series       []usage.Point   `json:"series"`
	TopReferrers []referrerCount `json:"top_referrers"`
	TopCountries []countryCount  `json:"top_countries"`
	TopPaths     []pathCount     `json:"top_paths"`
	TopDevices   []deviceCount   `json:"top_devices"`
	TopPages     *[]pageCount    `json:"top_pages,omitempty"`
}

// analyticsRange reads ?range=, answering the error to write when it is not
// one of usage.Ranges. Guests are refused: analytics come with an account.
func (s *server) analyticsRange(r *http.Request) (usage.Window, *apierr.Error) {
	if _, ok := guestFrom(r.Context()); ok {
		return usage.Window{}, apierr.Forbidden("guest pages have no analytics; claim them to see who viewed them")
	}
	rng, ok := usage.ParseRange(r.URL.Query().Get("range"))
	if !ok {
		return usage.Window{}, apierr.Invalid("range must be one of " + strings.Join(usage.Ranges, ", "))
	}
	return usage.WindowFor(rng, s.Now()), nil
}

func (s *server) artifactAnalytics(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	win, e := s.analyticsRange(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	rep, err := s.Store.Analytics(r.Context(), win.Query(a.TenantID, a.ID, analyticsTop))
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := s.report(&win, rep)
	out.ID, out.URL = a.ID, s.artifactViewURL(a)
	apierr.WriteJSON(w, 200, out)
}

func (s *server) accountAnalytics(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	win, e := s.analyticsRange(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	rep, err := s.Store.Analytics(r.Context(), win.Query(k.TenantID, "", analyticsTop))
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := s.report(&win, rep)
	pages := []pageCount{}
	for _, p := range rep.Pages {
		pages = append(pages, pageCount{ID: p.ArtifactID, Title: p.Title, URL: s.artifactViewURL(store.Artifact{ID: p.ArtifactID, Slug: p.Slug, Visibility: p.Visibility}),
			Views: p.Views, Visitors: p.Visitors})
	}
	out.TopPages = &pages
	for i := range out.TopPaths {
		out.TopPaths[i].ID = rep.Paths[i].ArtifactID
	}
	apierr.WriteJSON(w, 200, out)
}

func (s *server) report(win *usage.Window, rep store.Analytics) analyticsReport {
	series := win.Series(rep)
	out := analyticsReport{Range: win.Range, Bucket: "day", From: win.From, To: win.To, Series: series,
		Totals:       analyticsTotals{AllTimeViews: rep.AllTimeViews, RangeViews: rep.Views, RangeVisitors: rep.Visitors},
		TopReferrers: []referrerCount{}, TopCountries: []countryCount{}, TopPaths: []pathCount{}, TopDevices: []deviceCount{}}
	if win.Hourly {
		out.Bucket = "hour"
	}
	if rep.FirstDay != "" {
		out.FirstViewDay = &rep.FirstDay
	}
	for _, f := range rep.Referrers {
		out.TopReferrers = append(out.TopReferrers, referrerCount{Referrer: f.Value, Views: f.Views})
	}
	for _, f := range rep.Countries {
		out.TopCountries = append(out.TopCountries, countryCount{Country: f.Value, Views: f.Views})
	}
	for _, f := range rep.Paths {
		out.TopPaths = append(out.TopPaths, pathCount{Path: f.Value, Views: f.Views})
	}
	for _, f := range rep.Devices {
		out.TopDevices = append(out.TopDevices, deviceCount{Device: f.Value, Views: f.Views})
	}
	return out
}
