package app

import (
	"sort"
	"time"

	"github.com/anup-a/artifacts/internal/cover"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

// recentWindow is how far back the bento sections reach; anything published
// earlier goes to the plain list.
const recentWindow = 7 * 24 * time.Hour

// maxCardTags is how many tag chips a card shows in its meta row.
const maxCardTags = 2

// cardView is one bento tile: the artifact, its cover and the derived bits
// the template should not compute itself.
type cardView struct {
	Artifact store.Artifact
	URL      string
	CoverURL string
	Project  string // "" when unlabelled
	Color    string // project dot colour, shared with the cover generator
	Tags     []string
	Span     string // "hero", "wide" or ""
}

// sectionView is one day of recent artifacts. Label is the UTC label the
// server renders; At is the newest card's publish time so the client can
// relabel the section in the viewer's timezone.
type sectionView struct {
	Label string
	Day   string // YYYY-MM-DD in UTC
	At    time.Time
	Cards []cardView
}

func (h *Handler) card(a store.Artifact) cardView {
	c := cardView{Artifact: a, URL: h.shareURL(a.ID), CoverURL: serve.CoverURL(h.Cfg.ShareOrigin, a.ID), Project: deref(a.Project), Tags: a.Tags}
	c.Color = cover.ProjectColor(c.Project)
	if len(c.Tags) > maxCardTags {
		c.Tags = c.Tags[:maxCardTags]
	}
	return c
}

// splitRecent separates artifacts published within the window from the rest,
// keeping the store's order in both halves.
func splitRecent(list []store.Artifact, now time.Time) (recent, older []store.Artifact) {
	cutoff := now.Add(-recentWindow)
	for _, a := range list {
		if a.CreatedAt.After(cutoff) {
			recent = append(recent, a)
		} else {
			older = append(older, a)
		}
	}
	return recent, older
}

// bentoColumns is the grid width the shapes are fitted to; app.css draws the
// same four columns.
const bentoColumns = 4

// spans assigns the bento shape: with a hero the first card is 2 x 2 (four
// cells), every other card is one cell, and just enough of the trailing
// cards are made two columns wide that the last row ends flush instead of
// leaving a hole. Wide cards go at the end because, with dense packing, that
// is where the gap would otherwise appear.
func spans(cards []cardView, hero bool) []cardView {
	out := make([]cardView, len(cards))
	copy(out, cards)
	cells := len(cards)
	first := 0
	if hero && len(cards) > 0 {
		out[0].Span = "hero"
		cells += bentoColumns - 1 // a hero is four cells, not one
		first = 1
	}
	wide := (bentoColumns - cells%bentoColumns) % bentoColumns
	if wide > len(cards)-first {
		wide = len(cards) - first
	}
	for i := len(cards) - wide; i < len(cards); i++ {
		out[i].Span = "wide"
	}
	return out
}

// dayLabel names a UTC calendar day relative to now: Today, Yesterday, then
// "Mon 14 Sep".
func dayLabel(day, now time.Time) string {
	d := day.UTC().Truncate(24 * time.Hour)
	t := now.UTC().Truncate(24 * time.Hour)
	switch t.Sub(d) {
	case 0:
		return "Today"
	case 24 * time.Hour:
		return "Yesterday"
	}
	return d.Format("Mon 2 Jan")
}

// sections groups recent artifacts by UTC publish day, newest day first and
// newest artifact first inside a day. Only the first section gets a hero.
func (h *Handler) sections(recent []store.Artifact, now time.Time) []sectionView {
	byDay := map[string][]cardView{}
	for _, a := range recent {
		day := a.CreatedAt.UTC().Format("2006-01-02")
		byDay[day] = append(byDay[day], h.card(a))
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	out := make([]sectionView, 0, len(days))
	for i, day := range days {
		cards := byDay[day]
		sort.SliceStable(cards, func(x, y int) bool { return cards[x].Artifact.CreatedAt.After(cards[y].Artifact.CreatedAt) })
		at, _ := time.Parse("2006-01-02", day)
		out = append(out, sectionView{Label: dayLabel(at, now), Day: day, At: cards[0].Artifact.CreatedAt.UTC(), Cards: spans(cards, i == 0)})
	}
	return out
}

// grid is the filtered view: every recent result in one bento, no hero.
func (h *Handler) grid(recent []store.Artifact) []cardView {
	cards := make([]cardView, 0, len(recent))
	for _, a := range recent {
		cards = append(cards, h.card(a))
	}
	return spans(cards, false)
}
