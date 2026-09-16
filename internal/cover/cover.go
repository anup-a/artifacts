// Package cover renders deterministic 1200 x 630 SVG covers for artifacts
// that ship without one. The card follows the Vercel OG image idea: the text
// is the subject, the background is a quiet pattern. Plain shapes only, no
// <filter>, no external fonts, so a gallery of thumbnails paints fast. A cover
// is a pure function of its Card.
package cover

import (
	"hash/fnv"
	"html"
	"strconv"
	"strings"
	"time"
)

// Canvas size, the Open Graph image ratio.
const (
	Width  = 1200
	Height = 630
)

// Card is everything a generated cover shows.
type Card struct {
	ID    string
	Title string
	// Project is the label above the title; empty omits the line and makes
	// the accent neutral grey.
	Project string
	// Host is the share host shown in the footer, e.g. "byagent.dev".
	Host string
	// PublishedAt is the date in the footer; zero omits it.
	PublishedAt time.Time
	// Style is a generator name; unknown or empty falls back to PickStyle(ID).
	Style string
}

// styles ships in this order. All are selectable; only defaultStyles are
// handed out automatically, because dots, grid and wash are the three that
// read well at tile size (lines and rings go muddy, plain is a choice).
var styles = []string{"dots", "grid", "lines", "wash", "rings", "plain"}
var defaultStyles = []string{"dots", "grid", "wash"}

// Styles returns the generator names in their canonical order.
func Styles() []string {
	return append([]string(nil), styles...)
}

// Valid reports whether name is a known generator style.
func Valid(name string) bool {
	for _, s := range styles {
		if s == name {
			return true
		}
	}
	return false
}

// PickStyle derives the style for an artifact from its id, so an artifact
// keeps the same cover across renders and server restarts.
func PickStyle(id string) string {
	return defaultStyles[hash64(id)%uint64(len(defaultStyles))]
}

// Colours of the card. The ground is near white and the pattern is the
// foreground colour at low opacity, so the only saturated element is the
// project colour.
const (
	ground     = "#fafafa"
	foreground = "#171717"
	muted      = "#737373"
	neutral    = "#a3a3a3"
)

// projectPalette is the six-colour palette shared with the dashboard's
// project dots, so the card and the list agree on a project's colour.
var projectPalette = []string{"#2563eb", "#16a34a", "#d97706", "#dc2626", "#7c3aed", "#0891b2"}

// ProjectColor returns the stable colour for a project label, or neutral grey
// for no project. The dashboard uses the same function for its project dots.
func ProjectColor(project string) string {
	if project == "" {
		return neutral
	}
	return projectPalette[hash64(project)%uint64(len(projectPalette))]
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// Layout constants: title block vertically centred, everything left-aligned
// at textX, footer and accent bar on the bottom margin.
const (
	textX        = 80
	titleSize    = 60
	titleLeading = 72
	maxLineChars = 26
	maxLines     = 3
	projectSize  = 24
	projectGap   = 40 // from the project baseline down to the first title cap
	footerSize   = 22
	footerY      = Height - 64
	accentWidth  = 160
	accentHeight = 6
	fontStack    = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
)

// Render draws the cover for a card.
func Render(c Card) []byte {
	style := c.Style
	if !Valid(style) {
		style = PickStyle(c.ID)
	}
	title := c.Title
	if strings.TrimSpace(title) == "" {
		title = c.ID
	}
	accent := ProjectColor(c.Project)
	var b strings.Builder
	b.Grow(4096)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630" width="1200" height="630" role="img">`)
	b.WriteString(`<title>` + html.EscapeString(title) + `</title>`)
	b.WriteString(`<rect width="1200" height="630" fill="` + ground + `"/>`)
	drawPattern(&b, style, accent)
	drawText(&b, title, c.Project, footer(c.Host, c.PublishedAt), accent)
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

// footer joins the share host and the publish date, e.g. "byagent.dev · 16 Sep 2026".
func footer(host string, at time.Time) string {
	parts := []string{}
	if host != "" {
		parts = append(parts, host)
	}
	if !at.IsZero() {
		parts = append(parts, at.UTC().Format("2 Jan 2006"))
	}
	return strings.Join(parts, " · ")
}

func drawText(b *strings.Builder, title, project, foot, accent string) {
	lines := clampTitle(title)
	n := len(lines)
	// Baselines centred on the canvas; the offset nudges the visual centre of
	// the caps (which sit above the baseline) onto the midline.
	first := float64(Height)/2 - float64(n-1)*titleLeading/2 + titleSize*0.35
	if project != "" {
		py := first - titleSize - projectGap + projectSize
		b.WriteString(`<circle cx="` + f(textX+5) + `" cy="` + f(py-projectSize*0.32) + `" r="5" fill="` + accent + `"/>`)
		b.WriteString(`<text x="` + f(textX+22) + `" y="` + f(py) + `" font-family="` + fontStack + `" font-size="` + f(projectSize) + `" font-weight="500" fill="` + muted + `">` + html.EscapeString(project) + `</text>`)
	}
	for i, line := range lines {
		y := first + float64(i)*titleLeading
		b.WriteString(`<text x="` + f(textX) + `" y="` + f(y) + `" font-family="` + fontStack + `" font-size="` + f(titleSize) + `" font-weight="600" letter-spacing="-0.8" fill="` + foreground + `">` + html.EscapeString(line) + `</text>`)
	}
	if foot != "" {
		b.WriteString(`<text x="` + f(textX) + `" y="` + f(footerY) + `" font-family="` + fontStack + `" font-size="` + f(footerSize) + `" fill="` + muted + `">` + html.EscapeString(foot) + `</text>`)
	}
	b.WriteString(`<rect x="` + f(Width-textX-accentWidth) + `" y="` + f(footerY-accentHeight-2) + `" width="` + f(accentWidth) + `" height="` + f(accentHeight) + `" rx="3" fill="` + accent + `"/>`)
}

// clampTitle wraps on spaces into at most maxLines lines of maxLineChars
// runes, breaking over-long words and ending a truncated title with an
// ellipsis.
func clampTitle(title string) []string {
	var words []string
	for _, w := range strings.Fields(title) {
		for r := []rune(w); len(r) > maxLineChars; r = r[maxLineChars:] {
			words = append(words, string(r[:maxLineChars]))
			w = string(r[maxLineChars:])
		}
		if w != "" {
			words = append(words, w)
		}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		switch {
		case cur == "":
			cur = w
		case len([]rune(cur))+1+len([]rune(w)) <= maxLineChars:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
		if len(lines) == maxLines {
			rest := []rune(lines[maxLines-1])
			if len(rest) > maxLineChars-1 {
				rest = rest[:maxLineChars-1]
			}
			lines[maxLines-1] = strings.TrimRight(string(rest), " ") + "…"
			return lines
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// f formats a coordinate with one decimal, enough for a 1200 px canvas and
// short enough to keep covers small and byte-stable.
func f(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
