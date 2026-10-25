package app

import (
	"net/http"
	"time"

	"github.com/anup-a/byagent/internal/i18n"
	"github.com/anup-a/byagent/internal/store"
	"github.com/anup-a/byagent/internal/usage"
)

// The chart's drawing box. The SVG scales to the card's width; bars keep
// their proportions because the box is stretched, not the bars laid out.
const (
	chartW = 600.0
	chartH = 96.0
	// panelRange is the Views card's default: "this week".
	panelRange = "7d"
	panelTop   = 5
	// listViewsDays is the window of the view counts on the page list.
	listViewsDays = 30
)

// chartBar is one bucket of the Views chart, in chart units.
type chartBar struct {
	X, Y, W, H float64
	Title      string
}

// viewsPanel is the Views card on a page's detail view.
type viewsPanel struct {
	Range   string
	Ranges  []string
	Hourly  bool
	Views   int64
	Unique  int64
	AllTime int64
	Bars    []chartBar
	// From and To label the ends of the chart's axis.
	From, To                             string
	Referrers, Countries, Paths, Devices []store.FacetCount
}

// Empty is true when the range has no views, so the card can say so
// rather than draw a flat line.
func (p viewsPanel) Empty() bool { return p.Views == 0 }

// HasLists is true when any breakdown has something to show.
func (p viewsPanel) HasLists() bool {
	return len(p.Referrers)+len(p.Countries)+len(p.Paths)+len(p.Devices) > 0
}

func (h *Handler) viewsPanel(r *http.Request, a store.Artifact) (viewsPanel, error) {
	rng, ok := usage.ParseRange(r.URL.Query().Get("range"))
	if !ok || r.URL.Query().Get("range") == "" {
		rng = panelRange
	}
	win := usage.WindowFor(rng, h.Now())
	rep, err := h.Store.Analytics(r.Context(), win.Query(a.TenantID, a.ID, panelTop))
	if err != nil {
		return viewsPanel{}, err
	}
	series := win.Series(rep)
	p := viewsPanel{Range: rng, Ranges: usage.Ranges, Hourly: win.Hourly, Views: rep.Views, Unique: rep.Visitors, AllTime: rep.AllTimeViews,
		Referrers: rep.Referrers, Countries: rep.Countries, Paths: rep.Paths, Devices: rep.Devices}
	p.Bars, p.From, p.To = chart(series, win.Hourly, i18n.Lang(r))
	return p, nil
}

// chart turns a series into bars scaled to the busiest bucket, labelled in
// lang.
func chart(series []usage.Point, hourly bool, lang string) ([]chartBar, string, string) {
	if len(series) == 0 {
		return nil, "", ""
	}
	var max int64
	for _, p := range series {
		if p.Views > max {
			max = p.Views
		}
	}
	slot := chartW / float64(len(series))
	gap := slot * 0.2
	if gap > 4 {
		gap = 4
	}
	label := func(bucket string) string {
		if hourly {
			if t, err := time.Parse(time.RFC3339, bucket); err == nil {
				return i18n.T(lang, "%s, %s UTC", i18n.Day(lang, t), t.Format("15:00"))
			}
		}
		if t, err := time.Parse(time.DateOnly, bucket); err == nil {
			return i18n.Day(lang, t)
		}
		return bucket
	}
	bars := make([]chartBar, 0, len(series))
	for i, p := range series {
		hgt := 0.0
		if max > 0 && p.Views > 0 {
			hgt = chartH * float64(p.Views) / float64(max)
			if hgt < 2 {
				hgt = 2
			}
		}
		bars = append(bars, chartBar{X: float64(i)*slot + gap/2, Y: chartH - hgt, W: slot - gap, H: hgt,
			Title: label(p.Bucket) + ": " + i18n.N(lang, int(p.Views), "%s view", "%s views") + ", " + i18n.N(lang, int(p.Visitors), "%s visitor", "%s visitors")})
	}
	return bars, label(series[0].Bucket), label(series[len(series)-1].Bucket)
}

// listViews is each listed page's views over the last listViewsDays, for
// the counts on the page list. One grouped query covers the whole page.
func (h *Handler) listViews(r *http.Request, list []store.Artifact) map[string]int64 {
	ids := make([]string, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	from := h.Now().UTC().AddDate(0, 0, 1-listViewsDays).Format(time.DateOnly)
	counts, err := h.Store.ViewTotals(r.Context(), ids, from)
	if err != nil {
		// The counts are decoration; the list is still worth showing.
		return map[string]int64{}
	}
	return counts
}
