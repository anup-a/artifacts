package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"

	"github.com/anup-a/byagent/internal/i18n"
)

//go:embed static/app.css static/app.js static/theme.js static/ga.js static/i18n.js static/favicon.svg static/favicon.ico static/favicon-48.png static/favicon-192.png static/apple-touch-icon.png static/og-card.png static/landing.css static/landing.js static/dashboard-light*.webp static/dashboard-dark*.webp static/covers/*.webp
var staticFS embed.FS

type asset struct {
	body        []byte
	contentType string
}

// statics serves the stylesheet and script under content-hashed names so
// they can be cached forever and still change on deploy. The favicon is the
// exception: it sits at fixed paths (/favicon.svg, /favicon.ico and the PNGs)
// because published pages link to it by that path, browsers guess at
// /favicon.ico, and search engines skip an SVG-only icon for a generic globe.
type statics struct {
	files         map[string]asset
	cssURL        string
	jsURL         string
	themeJSURL    string            // resolves the landing page's theme before first paint
	gaJSURL       string            // starts GA4; a file because the CSP forbids inline script
	i18nJSURL     string            // t() and tn() for the other scripts
	landingCSSURL string            // the landing page's own styles, loaded on that page only
	landingJSURL  string            // its shelf, scroll effects and feature demos
	shotURL       string            // landing page dashboard screenshot, 1360 wide
	shotDarkURL   string            // the same view for a reader on a dark theme
	shotSet       string            // the screenshot at every width, for srcset
	shotDarkSet   string            // and the dark one
	coverURLs     map[string]string // page covers on the landing page, by name
	icons         map[string]asset  // the mark, by its fixed path
	// jsMessages are each language's translations of the messages the
	// scripts pass to t(), as the JSON block the layout puts on the page.
	jsMessages map[string]template.JS
}

// scripts are the files whose t("…") messages are handed to the page.
var scripts = []string{"static/app.js", "static/landing.js", "static/ga.js"}

// cardFile is the landing page's 1200 x 630 link preview card. It sits at a
// fixed root path like the icons rather than under /app/static: robots.txt
// disallows /app/, and the crawlers that build link previews honour that.
// The source is scripts/og-card.html, rendered with headless Chrome.
const cardFile = "og-card.png"

// iconFiles are the fixed-path renderings of the mark, by file name, plus the
// link preview card. The raster icons come from scripts/favicon.go.
var iconFiles = map[string]string{
	"favicon.svg":          "image/svg+xml",
	"favicon.ico":          "image/x-icon",
	"favicon-48.png":       "image/png",
	"favicon-192.png":      "image/png",
	"apple-touch-icon.png": "image/png",
	cardFile:               "image/png",
}

func newStatics() *statics {
	s := &statics{files: map[string]asset{}}
	s.cssURL = s.add("app", ".css", "text/css; charset=utf-8")
	s.jsURL = s.add("app", ".js", "text/javascript; charset=utf-8")
	s.themeJSURL = s.add("theme", ".js", "text/javascript; charset=utf-8")
	s.gaJSURL = s.add("ga", ".js", "text/javascript; charset=utf-8")
	s.i18nJSURL = s.add("i18n", ".js", "text/javascript; charset=utf-8")
	s.shotURL, s.shotSet = s.addWidths("dashboard-light")
	s.shotDarkURL, s.shotDarkSet = s.addWidths("dashboard-dark")
	s.landingCSSURL = s.add("landing", ".css", "text/css; charset=utf-8")
	s.landingJSURL = s.add("landing", ".js", "text/javascript; charset=utf-8")
	s.coverURLs = map[string]string{}
	covers, err := staticFS.ReadDir("static/covers")
	if err != nil {
		panic(err) // embedded, so only a build mistake
	}
	for _, c := range covers {
		name := strings.TrimSuffix(c.Name(), ".webp")
		s.coverURLs[name] = s.addAs("static/covers/"+c.Name(), "cover-"+name, ".webp", "image/webp")
	}
	var msgs []string
	for _, name := range scripts {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			panic(err) // embedded, so only a build mistake
		}
		msgs = append(msgs, i18n.FromJS(string(b))...)
	}
	s.jsMessages = map[string]template.JS{}
	for _, l := range i18n.Locales[1:] {
		b, err := json.Marshal(i18n.Messages(l.Code, msgs))
		if err != nil {
			panic(err)
		}
		s.jsMessages[l.Code] = template.JS(b)
	}
	s.icons = map[string]asset{}
	for name, ct := range iconFiles {
		body, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			panic(err) // embedded, so only a build mistake
		}
		s.icons["/"+name] = asset{body: body, contentType: ct}
	}
	return s
}

func (s *statics) add(base, ext, contentType string) string {
	return s.addAs("static/"+base+ext, base, ext, contentType)
}

// shotWidths are the widths a screenshot is saved at below base.webp, which
// is 2720 (twice the 1360 layout width), so a phone does not download the
// retina desktop file. Each is base-<width>.webp.
var shotWidths = []string{"720", "1080", "1360"}

// addWidths serves a screenshot at every width and returns the 1360 file for
// src and the srcset that lists them all.
func (s *statics) addWidths(base string) (src, srcset string) {
	for _, w := range shotWidths {
		u := s.add(base+"-"+w, ".webp", "image/webp")
		if w == "1360" {
			src = u
		}
		srcset += u + " " + w + "w, "
	}
	return src, srcset + s.add(base, ".webp", "image/webp") + " 2720w"
}

// addAs serves the embedded file at path under a hashed name built from base,
// for files in a subdirectory: the static route takes one path segment.
func (s *statics) addAs(path, base, ext, contentType string) string {
	body, err := staticFS.ReadFile(path)
	if err != nil {
		panic(err) // embedded, so only a build mistake
	}
	body = minified(body, contentType)
	sum := sha256.Sum256(body)
	name := base + "." + hex.EncodeToString(sum[:])[:12] + ext
	s.files[name] = asset{body: body, contentType: contentType}
	return "/app/static/" + name
}

// minifier strips the comments and whitespace from the stylesheets and
// scripts. The source files keep their comments; only what is served loses
// them, so there is no build step and nothing generated to commit.
var minifier = func() *minify.M {
	m := minify.New()
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("text/javascript", js.Minify)
	return m
}()

// minified is body minified for its content type, or body itself for a type
// the minifier does not know (images) or a file it cannot parse.
func minified(body []byte, contentType string) []byte {
	mt, _, _ := strings.Cut(contentType, ";")
	if mt != "text/css" && mt != "text/javascript" {
		return body
	}
	out, err := minifier.Bytes(mt, body)
	if err != nil {
		return body
	}
	return out
}

func (s *statics) serve(w http.ResponseWriter, r *http.Request) {
	a, ok := s.files[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(a.body)
}

// serveIcon answers /favicon.svg and the other fixed icon paths. The path is fixed rather than content
// hashed, so it can only be cached for a day: a mark change has to reach
// tabs without a new URL to bust them.
func (s *statics) serveIcon(w http.ResponseWriter, r *http.Request) {
	a, ok := s.icons[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(a.body)
}
