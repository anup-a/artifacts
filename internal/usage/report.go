package usage

import (
	"time"

	"github.com/anup-a/byagent/internal/store"
)

// DefaultRange is the report range when none is asked for.
const DefaultRange = "30d"

// Ranges are the report ranges, shortest first.
var Ranges = []string{"24h", "7d", "30d", "90d", "all"}

var rangeDays = map[string]int{"7d": 7, "30d": 30, "90d": 90}

// Window is the stretch of time a report covers, ending with the current
// hour (24h) or day (the rest), in UTC.
type Window struct {
	Range  string
	Hourly bool
	// From and To bound the series, To exclusive. For "all", From is the
	// first day with views and is only known once the report is read.
	From, To time.Time
}

// ParseRange accepts one of Ranges, or "" for DefaultRange.
func ParseRange(s string) (string, bool) {
	if s == "" {
		return DefaultRange, true
	}
	for _, r := range Ranges {
		if s == r {
			return s, true
		}
	}
	return "", false
}

// WindowFor is the window of range rng (already parsed) ending at now.
func WindowFor(rng string, now time.Time) Window {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	w := Window{Range: rng, To: today.AddDate(0, 0, 1)}
	switch rng {
	case "24h":
		w.Hourly = true
		w.To = now.Truncate(time.Hour).Add(time.Hour)
		w.From = w.To.Add(-24 * time.Hour)
	case "all":
		w.From = today
	default:
		w.From = today.AddDate(0, 0, 1-rangeDays[rng])
	}
	return w
}

// Query is the store query for this window: one page, or every page of the
// tenant when artifactID is empty. The facet lists of a 24h report cover
// the days its hours touch, today and yesterday.
func (w Window) Query(tenantID, artifactID string, top int) store.AnalyticsQuery {
	q := store.AnalyticsQuery{TenantID: tenantID, ArtifactID: artifactID, Hourly: w.Hourly, Top: top,
		FromDay: w.From.Format(time.DateOnly), ToDay: w.To.Format(time.DateOnly)}
	if w.Hourly {
		q.FromHour, q.ToHour = w.From.Format(hourFormat), w.To.Format(hourFormat)
		q.ToDay = w.To.Add(-time.Nanosecond).AddDate(0, 0, 1).Format(time.DateOnly)
	}
	if w.Range == "all" {
		q.FromDay = ""
	}
	return q
}

// Point is one bucket of a report's series. Bucket is the hour's start as
// RFC 3339 for 24h, else the UTC day.
type Point struct {
	Bucket   string `json:"bucket"`
	Views    int64  `json:"views"`
	Visitors int64  `json:"visitors"`
}

// Series lays a report's buckets over every hour or day of the window, with
// zeros where nothing was viewed. For "all" it starts at the first day with
// views; the window's From is moved there too.
func (w *Window) Series(a store.Analytics) []Point {
	have := map[string]store.AnalyticsPoint{}
	for _, p := range a.Series {
		have[p.Bucket] = p
	}
	if w.Range == "all" {
		if first, err := time.Parse(time.DateOnly, a.FirstDay); err == nil && first.Before(w.From) {
			w.From = first
		}
	}
	out := []Point{}
	if w.Hourly {
		for t := w.From; t.Before(w.To); t = t.Add(time.Hour) {
			p := have[t.Format(hourFormat)]
			out = append(out, Point{Bucket: t.Format(time.RFC3339), Views: p.Views, Visitors: p.Visitors})
		}
		return out
	}
	for t := w.From; t.Before(w.To); t = t.AddDate(0, 0, 1) {
		day := t.Format(time.DateOnly)
		p := have[day]
		out = append(out, Point{Bucket: day, Views: p.Views, Visitors: p.Visitors})
	}
	return out
}

// HourStart is the hour row key for t, for pruning.
func HourStart(t time.Time) string { return t.UTC().Format(hourFormat) }
