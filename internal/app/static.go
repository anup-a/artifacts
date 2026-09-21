package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
)

//go:embed static/app.css static/app.js static/theme.js static/dashboard-light.webp static/dashboard-dark.webp static/thread-light.webp static/thread-dark.webp
var staticFS embed.FS

type asset struct {
	body        []byte
	contentType string
}

// statics serves the stylesheet and script under content-hashed names so
// they can be cached forever and still change on deploy.
type statics struct {
	files         map[string]asset
	cssURL        string
	jsURL         string
	themeJSURL    string // resolves the landing page's theme before first paint
	shotURL       string // landing page dashboard screenshot
	shotDarkURL   string // the same view for a reader on a dark theme
	threadURL     string // a comment thread, inset over the dashboard
	threadDarkURL string
}

func newStatics() *statics {
	s := &statics{files: map[string]asset{}}
	s.cssURL = s.add("app", ".css", "text/css; charset=utf-8")
	s.jsURL = s.add("app", ".js", "text/javascript; charset=utf-8")
	s.themeJSURL = s.add("theme", ".js", "text/javascript; charset=utf-8")
	s.shotURL = s.add("dashboard-light", ".webp", "image/webp")
	s.shotDarkURL = s.add("dashboard-dark", ".webp", "image/webp")
	s.threadURL = s.add("thread-light", ".webp", "image/webp")
	s.threadDarkURL = s.add("thread-dark", ".webp", "image/webp")
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
