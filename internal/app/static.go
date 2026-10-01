package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
)

//go:embed static/app.css static/app.js static/theme.js static/ga.js static/favicon.svg static/favicon.ico static/favicon-48.png static/favicon-192.png static/apple-touch-icon.png static/og-card.png static/dashboard-light.webp static/dashboard-dark.webp static/thread-light.webp static/thread-dark.webp
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
	themeJSURL    string // resolves the landing page's theme before first paint
	gaJSURL       string // starts GA4; a file because the CSP forbids inline script
	shotURL       string // landing page dashboard screenshot
	shotDarkURL   string // the same view for a reader on a dark theme
	threadURL     string // a comment thread, inset over the dashboard
	threadDarkURL string
	icons         map[string]asset // the mark, by its fixed path
}

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
	s.shotURL = s.add("dashboard-light", ".webp", "image/webp")
	s.shotDarkURL = s.add("dashboard-dark", ".webp", "image/webp")
	s.threadURL = s.add("thread-light", ".webp", "image/webp")
	s.threadDarkURL = s.add("thread-dark", ".webp", "image/webp")
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
	body, err := staticFS.ReadFile("static/" + base + ext)
	if err != nil {
		panic(err) // embedded, so only a build mistake
	}
	sum := sha256.Sum256(body)
	name := base + "." + hex.EncodeToString(sum[:])[:12] + ext
	s.files[name] = asset{body: body, contentType: contentType}
	return "/app/static/" + name
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
