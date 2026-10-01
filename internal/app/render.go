package app

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/cover"
)

//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

var pageNames = []string{"landing", "login", "check_inbox", "artifacts", "artifact", "collections", "collection", "collection_public", "end_users", "keys", "usage", "settings", "portal", "error", "gate", "privacy", "terms", "security", "subprocessors", "acceptable_use", "pricing", "claim"}

// pages holds one template set per page, each sharing layout.html and the
// partials (icon sprite, shared rows).
var pages = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, name := range pageNames {
		out[name] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templateFS,
			"templates/layout.html", "templates/partials/*.html", "templates/"+name+".html"))
	}
	return out
}()

var funcs = template.FuncMap{
	"when":  formatTime,
	"ago":   relativeTime,
	"bytes": formatBytes,
	"num":   formatNumber,
	"str":   deref,
	"path":  url.PathEscape,
	// collectionURL is the dashboard page of one collection (project label).
	"collectionURL": collectionURL,
	// projectColor is the cover generator's palette, so dots match covers.
	"projectColor": cover.ProjectColor,
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
	// dict pairs the page with one item so a partial can reach both.
	"dict": func(p page, item any) map[string]any {
		return map[string]any{"Page": p, "Item": item}
	},
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// page is what every template receives.
type page struct {
	Title  string
	Path   string // current path, for the active nav entry
	Viewer *viewer
	CSS    string
	JS     string
	Now    time.Time // reference point for relative times
	Data   any
	// AnalyticsID is the GA4 Measurement ID for first-party app surfaces.
	// Empty skips the gtag snippet. GateArtifactAccess and /a/ denials leave
	// this unset so customer-facing share UI never loads analytics.
	AnalyticsID string
	GAJS        string // the script that starts GA4 with AnalyticsID
}

// Section is the nav entry the current path belongs to, so the detail page
// still highlights Artifacts.
func (p page) Section() string {
	switch {
	case strings.HasPrefix(p.Path, "/app/artifacts"):
		return "artifacts"
	case strings.HasPrefix(p.Path, "/app/collections"):
		return "collections"
	case strings.HasPrefix(p.Path, "/app/end-users"):
		return "end-users"
	case strings.HasPrefix(p.Path, "/app/keys"):
		return "keys"
	case strings.HasPrefix(p.Path, "/app/usage"):
		return "usage"
	case strings.HasPrefix(p.Path, "/app/settings"):
		return "settings"
	}
	return ""
}

// analyticsIDFor returns the configured GA4 ID for first-party app surfaces
// only. Published /a/… paths never get analytics, even when the env is set.
func (h *Handler) analyticsIDFor(path string) string {
	if strings.HasPrefix(path, "/a/") {
		return ""
	}
	return h.Cfg.GoogleAnalyticsID
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name, title string, v *viewer, data any) {
	h.renderRoot(w, r, status, name, "layout.html", title, v, data)
}

// renderRoot executes one named template of the page's set: "layout.html"
// for a full page, "panel" for the shell-less fragment the side peek loads.
func (h *Handler) renderRoot(w http.ResponseWriter, r *http.Request, status int, name, root, title string, v *viewer, data any) {
	p := page{Title: title, Path: r.URL.Path, Viewer: v, CSS: h.static.cssURL, JS: h.static.jsURL, Now: h.Now(), Data: data, AnalyticsID: h.analyticsIDFor(r.URL.Path), GAJS: h.static.gaJSURL}
	var buf bytes.Buffer
	if err := pages[name].ExecuteTemplate(&buf, root, p); err != nil {
		h.fail(w, r, fmt.Errorf("render %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

type errorData struct {
	Status int
	// Title replaces the status text as the heading, for a refusal whose
	// status code is chosen for what it withholds rather than what it means.
	Title     string
	Message   string
	RequestID string
	// SignIn offers a way in for someone who owns the artifact but is simply
	// signed out. Never set it where the message would confirm what exists.
	SignIn bool
}

// renderError shows msg with the request id for 4xx. Unexpected 5xx pass an
// empty msg and get a generic line instead of the error text.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if msg == "" {
		msg = "Something went wrong on our side. Try again in a moment."
	}
	h.renderErrorData(w, r, errorData{Status: status, Message: msg, RequestID: apierr.RequestID(r.Context())})
}

func (h *Handler) renderErrorData(w http.ResponseWriter, r *http.Request, d errorData) {
	status, msg := d.Status, d.Message
	title := d.Title
	if title == "" {
		title = http.StatusText(status)
	}
	p := page{Title: title, Path: r.URL.Path, CSS: h.static.cssURL, JS: h.static.jsURL, Now: h.Now(), Data: d, AnalyticsID: h.analyticsIDFor(r.URL.Path), GAJS: h.static.gaJSURL}
	var buf bytes.Buffer
	if err := pages["error"].ExecuteTemplate(&buf, "layout.html", p); err != nil {
		http.Error(w, msg, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// relativeTime renders t against now as "just now", "5m ago", "2h ago",
// "3d ago" or, beyond a month, the date. Future times read "in …".
func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	prefix, suffix := "", " ago"
	if d < 0 {
		d, prefix, suffix = -d, "in ", ""
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%s%dm%s", prefix, int(d/time.Minute), suffix)
	case d < 24*time.Hour:
		return fmt.Sprintf("%s%dh%s", prefix, int(d/time.Hour), suffix)
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%s%dd%s", prefix, int(d/(24*time.Hour)), suffix)
	}
	return t.UTC().Format("Jan 2, 2006")
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatNumber groups thousands with commas: 1234567 becomes 1,234,567.
func formatNumber(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var b []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, byte(c))
	}
	return neg + string(b)
}
