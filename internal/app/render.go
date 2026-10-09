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
	"github.com/anup-a/artifacts/internal/i18n"
)

//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

var pageNames = []string{"landing", "login", "check_inbox", "artifacts", "artifact", "collections", "collection", "collection_public", "end_users", "keys", "usage", "settings", "portal", "error", "gate", "privacy", "terms", "security", "subprocessors", "acceptable_use", "pricing", "showcase", "guide", "claim", "connect", "variables"}

// pages holds, for each language, one template set per page, each sharing
// layout.html and the partials (icon sprite, shared rows). The sets differ
// only in the language their t, tn, ago and num speak.
var pages = func() map[string]map[string]*template.Template {
	out := map[string]map[string]*template.Template{}
	for _, l := range i18n.Locales {
		set := map[string]*template.Template{}
		for _, name := range pageNames {
			set[name] = template.Must(template.New("layout.html").Funcs(funcs).Funcs(langFuncs(l.Code)).ParseFS(templateFS,
				"templates/layout.html", "templates/partials/*.html", "templates/"+name+".html"))
		}
		out[l.Code] = set
	}
	return out
}()

var funcs = template.FuncMap{
	"when":  formatTime,
	"bytes": formatBytes,
	"str":   deref,
	"path":  url.PathEscape,
	// collectionURL is the dashboard page of one collection (project label).
	"collectionURL": collectionURL,
	// projectColor is the cover generator's palette, so dots match covers.
	"projectColor": cover.ProjectColor,
	// initial is the first letter of a name, for an avatar.
	"initial": func(name string) string {
		for _, r := range strings.TrimSpace(name) {
			return strings.ToUpper(string(r))
		}
		return "?"
	},
	// dict pairs the page with one item so a partial can reach both.
	"dict": func(p page, item any) map[string]any {
		return map[string]any{"Page": p, "Item": item}
	},
}

// langFuncs are the template functions that speak lang:
//
//	{{t "Sign out"}}                      text, escaped like any other
//	{{th "Read <a href=\"%s\">it</a>" .URL}}  a message with markup; the
//	                                      message is trusted, string args are escaped
//	{{tn .Count "%s page" "%s pages"}}    the form for the count, with the count
//	                                      (a string, grouped: "1,234 pages")
func langFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(msg string, args ...any) string { return i18n.T(lang, msg, args...) },
		"th": func(msg string, args ...any) template.HTML {
			for i, a := range args {
				if s, ok := a.(string); ok {
					args[i] = template.HTMLEscapeString(s)
				}
			}
			return template.HTML(i18n.T(lang, msg, args...))
		},
		"tn": func(n any, one, other string, args ...any) string {
			return i18n.N(lang, toInt(n), one, other, args...)
		},
		"ago":   func(t, now time.Time) string { return i18n.Ago(lang, t, now) },
		"date":  func(t time.Time) string { return i18n.Date(lang, t) },
		"month": func(t time.Time) string { return i18n.Month(lang, t) },
		"num":   func(n int64) string { return i18n.Number(lang, n) },
	}
}

func toInt(n any) int {
	switch v := n.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	}
	return 0
}

// tr translates msg into the language r is answered in.
func tr(r *http.Request, msg string, args ...any) string {
	return i18n.T(i18n.Lang(r), msg, args...)
}

// trn is tr for a count: trn(r, n, "%d page", "%d pages").
func trn(r *http.Request, n int, one, other string, args ...any) string {
	return i18n.N(i18n.Lang(r), n, one, other, args...)
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
	// ThemeJS applies the saved light/dark choice before first paint.
	ThemeJS string
	Now     time.Time // reference point for relative times
	Data    any
	// AnalyticsID is the GA4 Measurement ID for first-party app surfaces.
	// Empty skips the gtag snippet. GateArtifactAccess and /a/ denials leave
	// this unset so customer-facing share UI never loads analytics.
	AnalyticsID string
	GAJS        string // the script that starts GA4 with AnalyticsID
	// Lang is the language the page is in.
	Lang      string
	langLinks []langLink
	// JSMessages are Lang's translations of the messages the scripts use,
	// as a JSON data block; empty in English.
	JSMessages template.JS
	I18nJS     string // byagentI18n.t for the scripts, loaded before them
}

// LangLinks is the current page in each language, for the picker.
func (p page) LangLinks() []langLink { return p.langLinks }

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
	case strings.HasPrefix(p.Path, "/app/connect"):
		return "connect"
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

// newPage is what every template receives, before the caller adds the
// viewer and the theme script.
func (h *Handler) newPage(r *http.Request, title string, data any) page {
	lang := i18n.Lang(r)
	return page{Title: title, Path: r.URL.Path, CSS: h.static.cssURL, JS: h.static.jsURL, Now: h.Now(), Data: data,
		AnalyticsID: h.analyticsIDFor(r.URL.Path), GAJS: h.static.gaJSURL,
		Lang: lang, langLinks: langLinks(r, h.Cfg.ShareOrigin), JSMessages: h.static.jsMessages[lang], I18nJS: h.static.i18nJSURL}
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name, title string, v *viewer, data any) {
	h.renderRoot(w, r, status, name, "layout.html", title, v, data)
}

// renderRoot executes one named template of the page's set: "layout.html"
// for a full page, "panel" for the shell-less fragment the side peek loads.
func (h *Handler) renderRoot(w http.ResponseWriter, r *http.Request, status int, name, root, title string, v *viewer, data any) {
	p := h.newPage(r, title, data)
	p.Viewer, p.ThemeJS = v, h.static.themeJSURL
	var buf bytes.Buffer
	if err := pages[p.Lang][name].ExecuteTemplate(&buf, root, p); err != nil {
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
		msg = tr(r, "Something went wrong on our side. Try again in a moment.")
	}
	h.renderErrorData(w, r, errorData{Status: status, Message: msg, RequestID: apierr.RequestID(r.Context())})
}

func (h *Handler) renderErrorData(w http.ResponseWriter, r *http.Request, d errorData) {
	status, msg := d.Status, d.Message
	title := d.Title
	if title == "" {
		title = tr(r, http.StatusText(status))
	}
	p := h.newPage(r, title, d)
	var buf bytes.Buffer
	if err := pages[p.Lang]["error"].ExecuteTemplate(&buf, "layout.html", p); err != nil {
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
