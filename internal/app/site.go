package app

import (
	"net/http"

	"github.com/anup-a/artifacts/internal/agents"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/pageurl"
)

// siteData is what the bar and the footer of every public page need: the
// landing page and the policy and pricing pages next to it. Origins carry no
// trailing slash.
type siteData struct {
	ShareOrigin string // the public site, what the canonical links point at
	AppURL      string // dashboard front door
	SkillURL    string // the agent skill this server serves
	AgentsURL   string // the read-only entrypoint written for agents
	ThemeJS     string // resolves the chosen theme before first paint
	Here        string // the path of the page being shown, for the current tab
	// PageForm is how a published page's address looks here, for prose:
	// https://<name>.byagent.dev/ with page hosts, else .../a/<id>/.
	// ContentDomain is set (byagent.dev or byagent.page) only when every page has a host
	// of its own, which the security and privacy pages describe.
	// ContentShared marks page hosts under byagent.dev itself
	// ({slug}.byagent.dev) rather than on a domain of their own.
	PageForm      string
	ContentDomain string
	ContentShared bool
}

func (h *Handler) site() siteData {
	return siteData{
		ShareOrigin:   h.Cfg.ShareOrigin,
		AppURL:        h.Cfg.APIOrigin + "/app/",
		SkillURL:      h.Cfg.ShareOrigin + "/skill.md",
		AgentsURL:     h.Cfg.ShareOrigin + "/agents",
		ThemeJS:       h.static.themeJSURL,
		PageForm:      h.pages().Form(),
		ContentDomain: pageurl.Hostname(h.Cfg.ContentDomain),
		ContentShared: h.Cfg.ContentShared,
	}
}

// Policy versions. Bump the version and the date together whenever the text
// of a policy changes in substance, and say what changed in the PR.
const (
	policyEffective = "5 October 2026"
	policyVersion   = "v1.4"
)

// docData is one policy or pricing page.
type docData struct {
	siteData
	Path      string // e.g. /privacy, for the canonical link
	Effective string
	Version   string
	Limits    config.Limits
}

// MB is the per-version size limit in whole megabytes, for prose.
func (d docData) MB() int64 { return d.Limits.VersionBytes / (1 << 20) }

// CommentKB is the comment size limit in whole kilobytes, for prose.
func (d docData) CommentKB() int { return d.Limits.CommentBytes / 1024 }

// docPages are the public pages that sit next to the landing page on the
// share origin, by path. The template name is the page's file name.
var docPages = map[string]struct{ name, title string }{
	"/privacy":        {"privacy", "Privacy"},
	"/terms":          {"terms", "Terms"},
	"/security":       {"security", "Security"},
	"/subprocessors":  {"subprocessors", "Subprocessors"},
	"/acceptable-use": {"acceptable_use", "Acceptable use"},
	"/pricing":        {"pricing", "Pricing"},
}

// doc renders one of docPages. Like the landing page it is the same for
// everyone, so it may sit in a shared cache.
func (h *Handler) doc(path string) http.HandlerFunc {
	p := docPages[path]
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		site := h.site()
		site.Here = path
		h.render(w, r, 200, p.name, p.title, nil, docData{
			siteData:  site,
			Path:      path,
			Effective: policyEffective,
			Version:   policyVersion,
			Limits:    h.Cfg.Limits,
		})
	}
}

// guideData is a guide page: the site's bar and footer around a guide the
// agents package rendered from its Markdown.
type guideData struct {
	siteData
	agents.GuideDoc
}

// GuidePage renders a guide, or the guides index, as a page of the site.
// The agents package owns the guides and their Markdown; this only dresses
// them like the pages around them.
func (h *Handler) GuidePage(w http.ResponseWriter, r *http.Request, doc agents.GuideDoc) {
	h.headers(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		title := doc.Guide.Title
		if doc.Index {
			title = "Guides"
		}
		site := h.site()
		site.Here = "/guides" // the Guides tab is current on every guide
		h.render(w, r, 200, "guide", title, nil, guideData{siteData: site, GuideDoc: doc})
	})(w, r)
}
