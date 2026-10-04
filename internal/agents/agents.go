// Package agents serves the read-only pages written for AI agents and
// crawlers rather than people: the /agents entrypoint (HTML and Markdown),
// one Markdown page per common task, a JSON fact sheet, /pricing.md,
// /llms.txt, /llms-full.txt, /robots.txt and /sitemap.xml. None of it needs
// a key.
//
// The product these pages describe is the byagent CLI. /skill.md stays the
// document a person pastes to their agent; these pages are the map around it.
package agents

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"net/http"
	"strings"
	"text/template"

	"github.com/anup-a/artifacts/internal/config"
)

//go:embed content/*.md content/*.txt content/*.html
var contentFS embed.FS

// Task is one job an agent commonly arrives to do, with its own page.
type Task struct {
	Slug    string // /agents/tasks/<slug>.md
	Title   string
	Summary string // one line, for tables and llms.txt
	Command string // the CLI line the task is built around
}

var tasks = []Task{
	{"setup", "Install the CLI and sign in", "Install byagent and store a key once, so every later command works.", "npm install -g byagent"},
	{"publish", "Publish a page", "Turn a Markdown file or an HTML directory into a shareable link.", "byagent publish ./notes.md --project \"<name>\" --tag notes --json"},
	{"read-comments", "Read comments on a page", "Fetch the open threads readers left on a published page.", "byagent comments <id> --open --json"},
	{"resolve-threads", "Reply to and resolve threads", "Act on a comment, answer it, and close the thread.", "byagent resolve <id> <thread> --json"},
	{"collections", "List collections", "Find the pages that belong together before publishing another one.", "byagent collections --json"},
	{"brief", "Start from a project brief", "Pick up work in a folder from its BRIEF.md and the open comments on it.", "byagent brief --json"},
}

// Entrypoint is one public URL an agent can fetch.
type Entrypoint struct {
	Path   string // relative to the share origin, or absolute when it lives elsewhere
	Format string
	What   string
}

// data is what every template sees. Origins carry no trailing slash.
type data struct {
	Share, API  string
	Tasks       []Task
	Entrypoints []Entrypoint
	Limits      config.Limits
	SignIn      bool
	Task        Task // set on a task page
	Guides      []Guide
	Guide       Guide // set on a guide page
}

// URL resolves an entrypoint path against the share origin.
func (d data) URL(p string) string {
	if strings.HasPrefix(p, "http") || strings.HasPrefix(p, "npm ") {
		return p
	}
	return d.Share + p
}

// MB is the per-version size limit in whole megabytes, for prose.
func (d data) MB() int64 { return d.Limits.VersionBytes / (1 << 20) }

// CommentKB is the comment size limit in whole kilobytes, for prose.
func (d data) CommentKB() int { return d.Limits.CommentBytes / 1024 }

const skillRepo = "https://github.com/anup-a/agent-artifacts"

func entrypoints(api string) []Entrypoint {
	return []Entrypoint{
		{"/agents", "HTML", "This page: what byagent is, how to reach it, and which page answers which task"},
		{"/agents.md", "Markdown", "The same page as Markdown (or GET /agents with Accept: text/markdown)"},
		{"/agents/facts.json", "JSON", "Identity, capabilities, limits and auth in one object"},
		{"/skill.md", "Markdown", "The skill to load into an agent: when to publish and every CLI rule"},
		{"/llms.txt", "Text", "A short map of these pages, llmstxt.org style"},
		{"/llms-full.txt", "Text", "This page and every task page in one file for bulk reading"},
		{"/robots.txt", "Text", "Crawl rules"},
		{"/sitemap.xml", "XML", "Every public page listed here"},
		{"npm install -g byagent", "CLI", "The CLI itself (or npx byagent <command>)"},
		{api + "/app/keys", "HTML", "Where a signed-in person creates the key the CLI asks for"},
		{skillRepo, "GitHub", "Canonical copy of the skill"},
	}
}

// Handler serves the agent pages for one deployment.
type Handler struct {
	Cfg config.Config

	d    data
	md   *template.Template
	html *htmltemplate.Template
}

func (h *Handler) Register(mux *http.ServeMux) {
	share := strings.TrimRight(h.Cfg.ShareOrigin, "/")
	api := strings.TrimRight(h.Cfg.APIOrigin, "/")
	h.d = data{Share: share, API: api, Tasks: tasks, Guides: guides, Entrypoints: entrypoints(api), Limits: h.Cfg.Limits, SignIn: h.Cfg.WebEnabled()}
	h.md = template.Must(template.New("").ParseFS(contentFS, "content/*.md", "content/*.txt"))
	h.html = htmltemplate.Must(htmltemplate.New("").ParseFS(contentFS, "content/*.html"))

	mux.HandleFunc("GET /agents", h.agents)
	mux.HandleFunc("GET /agents/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/agents", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /agents.md", h.text("agents.md", "text/markdown"))
	mux.HandleFunc("GET /agents/facts.json", h.facts)
	mux.HandleFunc("GET /agents/tasks/{file}", h.task)
	// The pricing page for agents. The page for people, /pricing, is in
	// internal/app with the rest of the site; both read the same limits.
	mux.HandleFunc("GET /pricing.md", h.text("pricing.md", "text/markdown"))
	mux.HandleFunc("GET /llms.txt", h.text("llms.txt", "text/plain"))
	mux.HandleFunc("GET /llms-full.txt", h.full)
	mux.HandleFunc("GET /robots.txt", h.text("robots.txt", "text/plain"))
	mux.HandleFunc("GET /sitemap.xml", h.text("sitemap.xml.txt", "application/xml"))
	// Guides for people searching for the problem, as HTML and Markdown.
	mux.HandleFunc("GET /guides", h.guidesIndex)
	mux.HandleFunc("GET /guides/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/guides", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /guides.md", h.text("guides.md", "text/markdown"))
	mux.HandleFunc("GET /guides/{file}", h.guideBySlug)
	mux.HandleFunc("GET /"+indexNowKey+".txt", func(w http.ResponseWriter, r *http.Request) {
		write(w, "text/plain", []byte(indexNowKey))
	})
}

// indexNowKey proves to IndexNow (Bing, Yandex, Seznam, Naver) that whoever
// submits byagent.dev URLs controls the site: the key is served at
// /<key>.txt, and scripts/indexnow.sh sends it with the URL list. It is
// public by design, not a secret.
const indexNowKey = "378018b2e38cfcb4209e8fb8eec6189e"

func (h *Handler) exec(name string, d data) ([]byte, error) {
	var b bytes.Buffer
	err := h.md.ExecuteTemplate(&b, name, d)
	return b.Bytes(), err
}

func write(w http.ResponseWriter, contentType string, body []byte) {
	// Public and only changes with a deploy, like /skill.md.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

func (h *Handler) text(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := h.exec(name, h.d)
		if err != nil {
			http.Error(w, "template error", 500)
			return
		}
		write(w, contentType, body)
	}
}

// wantsMarkdown reports whether the request names text/markdown ahead of
// text/html, which is how an agent that prefers Markdown asks for it.
func wantsMarkdown(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	md := strings.Index(accept, "text/markdown")
	if md < 0 {
		return false
	}
	html := strings.Index(accept, "text/html")
	return html < 0 || md < html
}

func (h *Handler) agents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", "Accept")
	if wantsMarkdown(r) {
		h.text("agents.md", "text/markdown")(w, r)
		return
	}
	var b bytes.Buffer
	if err := h.html.ExecuteTemplate(&b, "agents.html", h.d); err != nil {
		http.Error(w, "template error", 500)
		return
	}
	// Static prose with inline styles and no script at all.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Link", `</agents.md>; rel="alternate"; type="text/markdown"`)
	write(w, "text/html", b.Bytes())
}

func (h *Handler) task(w http.ResponseWriter, r *http.Request) {
	slug, ok := strings.CutSuffix(r.PathValue("file"), ".md")
	if ok {
		for _, t := range tasks {
			if t.Slug == slug {
				d := h.d
				d.Task = t
				body, err := h.exec("task-"+slug+".md", d)
				if err != nil {
					http.Error(w, "template error", 500)
					return
				}
				write(w, "text/markdown", body)
				return
			}
		}
	}
	http.NotFound(w, r)
}

// full is llms-full.txt: the entrypoint and every task page, so one fetch
// carries all of it.
func (h *Handler) full(w http.ResponseWriter, r *http.Request) {
	var out bytes.Buffer
	names := []string{"agents.md"}
	for _, t := range tasks {
		names = append(names, "task-"+t.Slug+".md")
	}
	for i, name := range names {
		d := h.d
		if i > 0 {
			d.Task = tasks[i-1]
		}
		body, err := h.exec(name, d)
		if err != nil {
			http.Error(w, "template error", 500)
			return
		}
		if i > 0 {
			out.WriteString("\n\n---\n\n")
		}
		out.Write(body)
	}
	// The skill is linked rather than inlined: it is the paste-in document
	// with its own rules, and it is one fetch away.
	out.WriteString("\n\n---\n\nThe full skill to load into an agent: " + h.d.Share + "/skill.md\n")
	write(w, "text/plain", out.Bytes())
}
