package agents

import (
	"bytes"
	"encoding/json"
	htmltemplate "html/template"
	"net/http"
	"strings"
)

// Guide is one page written for a person who has the problem byagent solves
// and is searching for it ("how do I share a Claude Code plan"), as opposed
// to the task pages, which assume the reader already has the CLI. Each guide
// is written once, as Markdown, and served at /guides/<slug> as HTML and at
// /guides/<slug>.md as the Markdown itself.
type Guide struct {
	Slug        string
	Title       string // the H1, the <title> and the link text
	Description string // meta description and the llms.txt line
	Updated     string // YYYY-MM-DD; bump it when the text changes in substance
}

var guides = []Guide{
	{"share-claude-code-plan", "Share a Claude Code plan as a link people can comment on", "Turn a plan, report or spec Claude Code wrote into a public or private link. Readers comment on the exact line, and Claude reads the comments back.", "2026-10-04"},
	{"codex", "Publish Codex output as a shareable link", "Give Codex CLI a way to hand back a link instead of a file path: install the byagent skill, publish Markdown or HTML, and read reader comments on the next run.", "2026-10-04"},
	{"cursor", "Share what Cursor's agent writes as a link", "Let Cursor's agent publish the reports, plans and HTML pages it writes as links your team can open and comment on without an account.", "2026-10-04"},
	{"host-agent-html", "Host an HTML page your agent built", "Put a dashboard, mockup or one-file HTML page an AI agent wrote at a stable link, with versions and comments, in one command.", "2026-10-04"},
	{"comments-back-to-agent", "Feed reader comments back to your coding agent", "Collect line-anchored feedback on a page an agent published, then have the agent read the open threads, fix the page, reply and resolve.", "2026-10-04"},
	{"alternatives", "Ways to share AI agent output, compared", "byagent, Claude Code Artifacts, Gist, Google Docs and static hosts compared for sharing what a coding agent writes and getting feedback.", "2026-10-04"},
}

func findGuide(slug string) (Guide, bool) {
	for _, g := range guides {
		if g.Slug == slug {
			return g, true
		}
	}
	return Guide{}, false
}

// GuideDoc is one guide, or the index, ready to render as a page: the
// Markdown after its title and summary line as HTML, and the JSON-LD.
type GuideDoc struct {
	Guide  Guide // the zero Guide on the index
	Index  bool
	Guides []Guide // every guide, for the index
	Path   string  // the canonical path, /guides or /guides/<slug>
	MDPath string  // the Markdown twin
	Body   htmltemplate.HTML
	LD     htmltemplate.JS
}

// guidePage is what the fallback guide.html sees.
type guidePage struct {
	GuideDoc
	Share string
}

func (h *Handler) guidesIndex(w http.ResponseWriter, r *http.Request) {
	h.guide(w, r, Guide{}, true)
}

func (h *Handler) guideBySlug(w http.ResponseWriter, r *http.Request) {
	slug, md := strings.CutSuffix(r.PathValue("file"), ".md")
	g, ok := findGuide(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if md {
		body, err := h.guideMarkdown(g)
		if err != nil {
			http.Error(w, "template error", 500)
			return
		}
		write(w, "text/markdown", body)
		return
	}
	h.guide(w, r, g, false)
}

func (h *Handler) guideMarkdown(g Guide) ([]byte, error) {
	d := h.d
	d.Guide = g
	return h.exec("guide-"+g.Slug+".md", d)
}

func (h *Handler) guide(w http.ResponseWriter, r *http.Request, g Guide, index bool) {
	w.Header().Set("Vary", "Accept")
	d := h.d
	d.Guide = g
	name, path, mdPath := "guide-"+g.Slug+".md", "/guides/"+g.Slug, "/guides/"+g.Slug+".md"
	if index {
		name, path, mdPath = "guides.md", "/guides", "/guides.md"
	}
	md, err := h.exec(name, d)
	if err != nil {
		http.Error(w, "template error", 500)
		return
	}
	if wantsMarkdown(r) {
		write(w, "text/markdown", md)
		return
	}
	ld, err := guideLD(h.d.Share, g, path, index)
	if err != nil {
		http.Error(w, "template error", 500)
		return
	}
	doc := GuideDoc{Guide: g, Index: index, Guides: guides, Path: path, MDPath: mdPath,
		Body: htmltemplate.HTML(markdownToHTML(afterHead(string(md)))), LD: htmltemplate.JS(ld)}
	w.Header().Set("Link", `<`+mdPath+`>; rel="alternate"; type="text/markdown"`)
	// On a deployment with the web app, the guide is a page of the site,
	// with its bar, theme and footer. Without it, a plain page.
	if h.GuidePage != nil {
		h.GuidePage(w, r, doc)
		return
	}
	var b bytes.Buffer
	if err := h.html.ExecuteTemplate(&b, "guide.html", guidePage{GuideDoc: doc, Share: h.d.Share}); err != nil {
		http.Error(w, "template error", 500)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	write(w, "text/html", b.Bytes())
}

// afterHead drops a guide's "# Title" line and the "> summary" under it,
// which the page shows in its own header.
func afterHead(md string) string {
	lines := strings.Split(md, "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.HasPrefix(lines[i], "# ") {
		i++
	}
	for i < len(lines) && (strings.TrimSpace(lines[i]) == "" || strings.HasPrefix(lines[i], ">")) {
		i++
	}
	return strings.Join(lines[i:], "\n")
}

// guideLD is the schema.org description of a guide: the article and the
// breadcrumb trail back to the home page.
func guideLD(share string, g Guide, path string, index bool) ([]byte, error) {
	crumbs := []map[string]any{
		{"@type": "ListItem", "position": 1, "name": "byagent", "item": share + "/"},
		{"@type": "ListItem", "position": 2, "name": "Guides", "item": share + "/guides"},
	}
	var main map[string]any
	if index {
		main = map[string]any{"@type": "CollectionPage", "name": "byagent guides", "url": share + path}
	} else {
		crumbs = append(crumbs, map[string]any{"@type": "ListItem", "position": 3, "name": g.Title, "item": share + path})
		main = map[string]any{
			"@type":         "TechArticle",
			"headline":      g.Title,
			"description":   g.Description,
			"url":           share + path,
			"dateModified":  g.Updated,
			"datePublished": g.Updated,
			"author":        map[string]any{"@type": "Organization", "name": "byagent", "url": share + "/"},
			"about":         map[string]any{"@type": "SoftwareApplication", "name": "byagent", "url": share + "/"},
		}
	}
	return json.Marshal(map[string]any{
		"@context": "https://schema.org",
		"@graph":   []any{main, map[string]any{"@type": "BreadcrumbList", "itemListElement": crumbs}},
	})
}
