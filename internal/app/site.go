package app

import (
	"net/http"

	"github.com/anup-a/byagent/internal/agents"
	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/i18n"
	"github.com/anup-a/byagent/internal/pageurl"
)

// siteData is what the bar and the footer of every public page need: the
// landing page and the policy and pricing pages next to it. Origins carry no
// trailing slash.
type siteData struct {
	ShareOrigin string // the public site, what the canonical links point at
	AppURL      string // dashboard front door
	SkillURL    string // the agent skill this server serves
	AgentsURL   string // the read-only entrypoint written for agents
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
	// AnalyticsOn: GA is configured, so the footer offers "Cookie settings"
	// to change the choice made in the consent banner.
	AnalyticsOn bool
	// LangLinks is this page in each language, for the footer's picker and
	// the hreflang links in the head.
	LangLinks []langLink
	// Translated marks a page whose body is translated, not just its bar
	// and footer, so search engines are told about each language's
	// address. The policy pages and guides are English only.
	Translated bool
}

// Canonical is the address search engines should index for this page: its
// own language's on a translated page, else the English one.
func (s siteData) Canonical() string {
	for _, l := range s.LangLinks {
		if l.Current && s.Translated {
			return l.Alternate
		}
	}
	for _, l := range s.LangLinks {
		if l.Code == i18n.Default {
			return l.Alternate
		}
	}
	return s.ShareOrigin
}

// langLink is the current page in one language.
type langLink struct {
	Code, Name string
	URL        string // relative, for the picker: always names the language
	Alternate  string // absolute, for hreflang: English is the bare address
	Current    bool
}

// langLinks is path in every language. query is the rest of the request's
// query, kept so the picker does not drop a filter or a search.
func langLinks(r *http.Request, origin string) []langLink {
	cur := i18n.Lang(r)
	q := r.URL.Query()
	q.Del("lang")
	rest := q.Encode()
	if rest != "" {
		rest += "&"
	}
	out := make([]langLink, 0, len(i18n.Locales))
	for _, l := range i18n.Locales {
		out = append(out, langLink{Code: l.Code, Name: l.Name, Current: l.Code == cur,
			URL: r.URL.Path + "?" + rest + "lang=" + l.Code, Alternate: origin + i18n.URL(r.URL.Path, l.Code)})
	}
	return out
}

func (h *Handler) site(r *http.Request) siteData {
	return siteData{
		LangLinks:     langLinks(r, h.Cfg.ShareOrigin),
		ShareOrigin:   h.Cfg.ShareOrigin,
		AppURL:        h.Cfg.APIOrigin + "/app/",
		SkillURL:      h.Cfg.ShareOrigin + "/skill.md",
		AgentsURL:     h.Cfg.ShareOrigin + "/agents",
		PageForm:      h.pages().Form(),
		ContentDomain: pageurl.Hostname(h.Cfg.ContentDomain),
		ContentShared: h.Cfg.ContentShared,
		AnalyticsOn:   h.Cfg.GoogleAnalyticsID != "",
	}
}

// Policy versions. Bump the version and the date together whenever the text
// of a policy changes in substance, and say what changed in the PR.
const (
	policyEffective = "9 October 2026"
	policyVersion   = "v1.7"
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
// share origin, by path. The template name is the page's file name, and
// the title is translated where the page is shown.
var docPages = map[string]struct{ name, title string }{
	"/privacy":        {"privacy", i18n.M("Privacy")},
	"/terms":          {"terms", i18n.M("Terms")},
	"/security":       {"security", i18n.M("Security")},
	"/subprocessors":  {"subprocessors", i18n.M("Subprocessors")},
	"/acceptable-use": {"acceptable_use", i18n.M("Acceptable use")},
	"/pricing":        {"pricing", i18n.M("Pricing")},
	"/about":          {"about", i18n.M("About")},
}

// doc renders one of docPages. Like the landing page it is the same for
// everyone, so it may sit in a shared cache.
func (h *Handler) doc(path string) http.HandlerFunc {
	p := docPages[path]
	return func(w http.ResponseWriter, r *http.Request) {
		publicCache(w, r)
		site := h.site(r)
		site.Here = path
		site.Translated = path == "/pricing"
		h.render(w, r, 200, p.name, tr(r, p.title), nil, docData{
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
		publicCache(w, r)
		title := doc.Guide.Title
		if doc.Index {
			title = tr(r, "Guides")
		}
		site := h.site(r)
		site.Here = "/guides" // the Guides tab is current on every guide
		h.render(w, r, 200, "guide", title, nil, guideData{siteData: site, GuideDoc: doc})
	})(w, r)
}
