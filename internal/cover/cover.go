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
	"unicode"
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
var defaultStyles = []string{"dots", "grid"}

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

// The same card on a dark page. A cover is shown inside a dashboard tile and
// as the page's og:image, so it cannot be near-white in both: on the dark
// dashboard a row of them reads as lit slabs. The ground sits a shade above
// the dark panel it lands on, the way the light ground sits a shade below the
// white one.
const (
	darkGround     = "#181818"
	darkForeground = "#ededed"
	darkMuted      = "#8f8f8f"
	darkNeutral    = "#6b6b6b"
)

// scheme is the one stylesheet a cover carries. Elements keep their light
// colours as presentation attributes, which a stylesheet-aware renderer
// overrides and a plain rasteriser (a social crawler, an SVG-to-PNG step)
// still honours, so the light card remains the default everywhere.
const scheme = `<style>` +
	`@media(prefers-color-scheme:dark){` +
	`.bg{fill:` + darkGround + `}` +
	`.tt{fill:` + darkForeground + `}` +
	`.mu{fill:` + darkMuted + `}` +
	`.nu{fill:` + darkNeutral + `}` +
	// Split by property: the stroked patterns carry fill="none", and a rule
	// that set fill as well would flood them.
	`.pf{fill:` + darkForeground + `}` +
	`.ps{stroke:` + darkForeground + `}` +
	`}</style>`

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
	textX = 80
	// 62px keeps the title readable when the whole card is shown at a
	// 233px-wide dashboard tile (about 12px there) and is the largest size
	// at which 26 bold characters still fit inside the 80px margins.
	titleSize    = 62
	titleLeading = 74
	maxLineChars = 26
	maxLines     = 3
	projectSize  = 24
	projectGap   = 40 // from the project baseline down to the first title cap
	footerSize   = 22
	footerY      = Height - 64
	accentWidth  = 160
	accentHeight = 6
	// The CJK faces come after the Latin ones, so Latin titles look as they
	// always did and Chinese, Japanese and Korean ones get a real face.
	fontStack = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, 'PingFang SC', 'Hiragino Sans', 'Microsoft YaHei', 'Noto Sans CJK SC', sans-serif"
)

// Render draws the cover for a card.
func Render(c Card) []byte {
	style := c.Style
	if !Valid(style) {
		style = PickStyle(c.ID)
	}
	// "wash" means the tint alone; without a project there is no colour to
	// tint with and a grey gradient reads as a smudge, so those cards get dots.
	hasProject := strings.TrimSpace(c.Project) != ""
	if style == "wash" && !hasProject {
		style = "dots"
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
	b.WriteString(scheme)
	b.WriteString(`<rect class="bg" width="1200" height="630" fill="` + ground + `"/>`)
	if hasProject {
		drawWash(&b, accent)
	}
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
		b.WriteString(`<text class="mu" x="` + f(textX+22) + `" y="` + f(py) + `" font-family="` + fontStack + `" font-size="` + f(projectSize) + `" font-weight="500" fill="` + muted + `">` + html.EscapeString(project) + `</text>`)
	}
	for i, line := range lines {
		y := first + float64(i)*titleLeading
		b.WriteString(`<text class="tt" x="` + f(textX) + `" y="` + f(y) + `" font-family="` + fontStack + `" font-size="` + f(titleSize) + `" font-weight="600" letter-spacing="-0.8" fill="` + foreground + `">` + html.EscapeString(line) + `</text>`)
	}
	if foot != "" {
		b.WriteString(`<text class="mu" x="` + f(textX) + `" y="` + f(footerY) + `" font-family="` + fontStack + `" font-size="` + f(footerSize) + `" fill="` + muted + `">` + html.EscapeString(foot) + `</text>`)
	}
	// A card with no project has a grey bar rather than a project colour, and
	// grey is the one accent that has to dim on a dark ground.
	barClass := ""
	if accent == neutral {
		barClass = ` class="nu"`
	}
	b.WriteString(`<rect` + barClass + ` x="` + f(Width-textX-accentWidth) + `" y="` + f(footerY-accentHeight-2) + `" width="` + f(accentWidth) + `" height="` + f(accentHeight) + `" rx="3" fill="` + accent + `"/>`)
}

// clampTitle wraps a title into at most maxLines lines no wider than
// maxLineChars Latin characters, breaking over-long words and ending a
// truncated title with an ellipsis. Latin text wraps on spaces. Chinese,
// Japanese and Korean have no spaces to wrap on and each character is about
// wideWidth Latin ones across, so they wrap between any two characters,
// keeping closing punctuation off the start of a line.
func clampTitle(title string) []string {
	const limit = maxLineChars * narrowWidth
	var toks []token
	for _, w := range strings.Fields(title) {
		for i, t := range splitWord(w) {
			t.space = i == 0
			toks = append(toks, t)
		}
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, t := range toks {
		sep := 0
		if t.space {
			sep = narrowWidth
		}
		switch {
		case curW == 0:
			cur.WriteString(t.text)
			curW = t.width
		case curW+sep+t.width <= limit:
			if t.space {
				cur.WriteByte(' ')
			}
			cur.WriteString(t.text)
			curW += sep + t.width
		default:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(t.text)
			curW = t.width
		}
		if len(lines) == maxLines {
			lines[maxLines-1] = strings.TrimRight(fit(lines[maxLines-1], limit-narrowWidth), " ") + "…"
			return lines
		}
	}
	if curW > 0 {
		lines = append(lines, cur.String())
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// Widths in tenths of an average Latin character at the title size.
const (
	narrowWidth = 10
	wideWidth   = 16
)

// token is a piece of a title that stays on one line. space says a space
// came before it; pieces of one Chinese word join without one.
type token struct {
	text  string
	width int
	space bool
}

// splitWord cuts one space-free word into the pieces a line may break
// between: each wide character on its own, runs of Latin text whole but no
// longer than a line, punctuation held to its neighbour.
func splitWord(w string) []token {
	var out []token
	var run []rune
	flush := func() {
		for len(run) > maxLineChars {
			out = append(out, token{text: string(run[:maxLineChars]), width: maxLineChars * narrowWidth, space: true})
			run = run[maxLineChars:]
		}
		if len(run) > 0 {
			out = append(out, token{text: string(run), width: len(run) * narrowWidth})
			run = nil
		}
	}
	opener := false
	for _, r := range w {
		switch {
		case !wide(r):
			run = append(run, r)
		case strings.ContainsRune(closers, r) && (len(out) > 0 || len(run) > 0):
			flush()
			out[len(out)-1].text += string(r)
			out[len(out)-1].width += wideWidth
		default:
			flush()
			if opener {
				out[len(out)-1].text += string(r)
				out[len(out)-1].width += wideWidth
			} else {
				out = append(out, token{text: string(r), width: wideWidth})
			}
			opener = strings.ContainsRune(openers, r)
			continue
		}
		opener = false
	}
	flush()
	return out
}

// Punctuation that may not end a line (openers) or start one (closers).
const (
	openers = "（「『《〈【〔“‘"
	closers = "，。、；：！？）」』》〉】〕”’…ー々"
)

// wide reports whether r is a Chinese, Japanese or Korean character or
// full-width form, which takes about a square of the line.
func wide(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		(r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef)
}

// fit cuts s to at most limit tenths wide.
func fit(s string, limit int) string {
	w := 0
	for i, r := range s {
		n := narrowWidth
		if wide(r) {
			n = wideWidth
		}
		if w+n > limit {
			return s[:i]
		}
		w += n
	}
	return s
}

// f formats a coordinate with one decimal, enough for a 1200 px canvas and
// short enough to keep covers small and byte-stable.
func f(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
