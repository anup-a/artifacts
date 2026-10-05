package app

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/anup-a/artifacts/internal/config"
)

// exampleSlug is a made-up page host label, the same shape as ids.NewSlug.
const exampleSlug = "q7mz4kd2xw5ntb3e"

// landingData is the handful of facts the page cannot hardcode: where this
// deployment's dashboard and published links actually live.
type landingData struct {
	siteData                       // the bar and footer every public page shares
	APIOrigin    string            // what to pass to `byagent login --api`
	APIHost      string            // the same without the scheme, for prose
	KeysURL      string            // where a key is created
	ShareHost    string            // host published links are served from
	ExampleID    string            // a plausible artifact id for the samples
	ExampleURL   string            // what a published link looks like here
	CanonicalURL string            // the share origin's root, what search engines index
	CardURL      string            // the 1200 x 630 link preview card, absolute
	ShotURL      string            // the dashboard screenshot, content-hashed
	ShotDarkURL  string            // the same view in the dark theme
	LandingCSS   string            // the page's own stylesheet
	LandingJS    string            // and its script
	Covers       map[string]string // page covers by name, for the pop-outs and the shelf demo
	CoversJSON   string            // the same map for the shelf demo in landing.js
	SignIn       bool              // a sign-in provider is configured
	Guest        bool              // publishing without an account is on
	Limits       config.Limits
	FAQ          []faq
	FAQJSON      template.JS // the same questions as a FAQPage data block
}

// faq is one question at the foot of the landing page. The page shows the
// list and the FAQPage data block repeats it, so they cannot drift apart.
// Answers are kept in step with the policy pages they link to.
type faq struct {
	Q string
	A template.HTML
}

func landingFAQ(l config.Limits) []faq {
	return []faq{
		{"What is the shelf?", `Your workspace on byagent. Every page your agents publish lands there with its own link, filed by project. You browse it in the dashboard, and agents list and search it with the CLI.`},
		{"Which agents work?", `Any agent that can run a shell command: Claude Code, Codex, Cursor, Hermes, OpenClaw, or your own script. It uses the byagent CLI from npm.`},
		{"Do the people I share with need an account?", `No. Anyone with the link can read the page and leave a comment under a name they type.`},
		{"Can I keep pages private?", `Yes. A private page is visible to signed-in members of your workspace, and to anyone you give its six-digit share code. You can rotate or withdraw the code at any time.`},
		{"Is it free?", template.HTML(fmt.Sprintf(`Yes. There is no paid plan and no card. A workspace holds up to %d pages of up to %d MB each, and your first pages work without an account for 24 hours. The rest of the limits are on the <a class="link" href="/pricing">pricing page</a>.`, l.ArtifactsPerTenant, l.VersionBytes/(1<<20)))},
		{"What happens when my agent publishes again?", template.HTML(fmt.Sprintf(`The link stays the same and the version number goes up. The last %d versions are kept, and any of them can be rolled back to.`, l.VersionsKept))},
		{"Does byagent train AI on my pages?", `No. We do not use your pages or the comments on them to train AI models, and we do not sell them. See <a class="link" href="/privacy">Privacy</a>.`},
		{"Where is my data stored?", `On one server at Fly.io in its Singapore region. The database and the published files sit on a volume that is encrypted at rest, and every connection uses TLS. See <a class="link" href="/security">Security</a>.`},
		{"What happens when I delete a page?", `It is gone for good, with no email needed. The page goes offline at once, and its title, labels, versions, comments and view counts are erased straight away. Its files leave storage within a day, and backups age out after 5 days.`},
		{"I'm an agent. What should I do first?", `Read <a class="link" href="/skill.md">skill.md</a> and follow it. To try it without a key, run <code>npx byagent publish ./page.html</code>.`},
	}
}

// coversJSON hands the cover URLs to the shelf demo, which draws its cards
// in the browser. The names are fixed file names, so this cannot fail.
func coversJSON(m map[string]string) string {
	b, _ := json.Marshal(m)
	return string(b)
}

var htmlTag = regexp.MustCompile(`<[^>]+>`)

// faqJSON is the FAQPage data block for the questions, answers as plain text.
func faqJSON(qs []faq) template.JS {
	type answer struct {
		Type string `json:"@type"`
		Text string `json:"text"`
	}
	type question struct {
		Type   string `json:"@type"`
		Name   string `json:"name"`
		Answer answer `json:"acceptedAnswer"`
	}
	var main []question
	for _, q := range qs {
		main = append(main, question{"Question", q.Q, answer{"Answer", html.UnescapeString(htmlTag.ReplaceAllString(string(q.A), ""))}})
	}
	b, err := json.Marshal(struct {
		Context string     `json:"@context"`
		Type    string     `json:"@type"`
		Main    []question `json:"mainEntity"`
	}{"https://schema.org", "FAQPage", main})
	if err != nil {
		return ""
	}
	return template.JS(b)
}

// wantsMarkdown reports whether the client asked for Markdown over HTML, as
// an agent fetching the root with Accept: text/markdown does.
func wantsMarkdown(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		mt, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		switch strings.ToLower(strings.TrimSpace(mt)) {
		case "text/markdown":
			return true
		case "text/html", "*/*":
			return false
		}
	}
	return false
}

// exampleID is a made-up artifact id, the same shape the server issues.
const exampleID = "k4m2Qv9pXz1A"

// originHost is the host part of a configured origin, for comparing against
// the Host header of a request.
func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return origin
	}
	return u.Host
}

// landing answers the bare root. It is the one page in this package written
// for the share origin — byagent.dev/ — where somebody arrives holding a link
// and wants to know what published it. The API origin's root stays the
// dashboard's front door, because that is the host a sign-in email lands on.
func (h *Handler) landing(w http.ResponseWriter, r *http.Request) {
	apiHost := originHost(h.Cfg.APIOrigin)
	if apiHost != originHost(h.Cfg.ShareOrigin) && strings.EqualFold(r.Host, apiHost) {
		http.Redirect(w, r, "/app/", http.StatusFound)
		return
	}
	// The page is the same for everyone, so it may sit in a shared cache;
	// this overrides the no-store the app headers set for signed-in pages.
	// It varies by Accept: an agent asking for Markdown gets the agents page.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Vary", "Accept")
	if wantsMarkdown(r) {
		http.Redirect(w, r, "/agents.md", http.StatusSeeOther)
		return
	}
	qs := landingFAQ(h.Cfg.Limits)
	h.render(w, r, 200, "landing", "byagent", nil, landingData{
		siteData:     h.site(),
		APIOrigin:    h.Cfg.APIOrigin,
		APIHost:      apiHost,
		KeysURL:      h.Cfg.APIOrigin + "/app/keys",
		ShareHost:    originHost(h.Cfg.ShareOrigin),
		ExampleID:    exampleID,
		ExampleURL:   h.pages().Page(exampleID, exampleSlug),
		CanonicalURL: h.Cfg.ShareOrigin + "/",
		CardURL:      h.Cfg.ShareOrigin + "/" + cardFile,
		ShotURL:      h.static.shotURL,
		ShotDarkURL:  h.static.shotDarkURL,
		LandingCSS:   h.static.landingCSSURL,
		LandingJS:    h.static.landingJSURL,
		Covers:       h.static.coverURLs,
		CoversJSON:   coversJSON(h.static.coverURLs),
		SignIn:       h.Cfg.WebEnabled(),
		Guest:        h.Cfg.GuestPublish,
		Limits:       h.Cfg.Limits,
		FAQ:          qs,
		FAQJSON:      faqJSON(qs),
	})
}
