package app

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
)

//go:embed templates/*.html
var templateFS embed.FS

var pageNames = []string{"login", "check_inbox", "artifacts", "end_users", "keys", "usage", "settings", "portal", "error"}

// pages holds one template set per page, each sharing layout.html.
var pages = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, name := range pageNames {
		out[name] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
	}
	return out
}()

var funcs = template.FuncMap{
	"when":  formatTime,
	"bytes": formatBytes,
	"num":   formatNumber,
	"str":   deref,
	"path":  url.PathEscape,
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
	Data   any
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name, title string, v *viewer, data any) {
	p := page{Title: title, Path: r.URL.Path, Viewer: v, CSS: h.static.cssURL, JS: h.static.jsURL, Data: data}
	var buf bytes.Buffer
	if err := pages[name].ExecuteTemplate(&buf, "layout.html", p); err != nil {
		h.fail(w, r, fmt.Errorf("render %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

type errorData struct {
	Status    int
	Message   string
	RequestID string
}

// renderError shows msg with the request id for 4xx. Unexpected 5xx pass an
// empty msg and get a generic line instead of the error text.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if msg == "" {
		msg = "Something went wrong on our side. Try again in a moment."
	}
	d := errorData{Status: status, Message: msg, RequestID: apierr.RequestID(r.Context())}
	p := page{Title: http.StatusText(status), Path: r.URL.Path, CSS: h.static.cssURL, JS: h.static.jsURL, Data: d}
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
