package app

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// getHost fetches a path with an explicit Host header, which is how the one
// server tells the share origin from the API origin.
func (e *env) getHost(host, path string) reply {
	e.t.Helper()
	req, err := http.NewRequest("GET", e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = host
	res, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return reply{res, string(b)}
}

func TestLandingServesThePublishingPage(t *testing.T) {
	e := fixture(t)
	res := e.getHost("share.test", "/")
	if res.StatusCode != 200 {
		t.Fatalf("got %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	for _, want := range []string{
		"A shareable link</span> <span class=\"nw\">for everything</span>", // the job, not a metaphor
		"your agents make.",
		"whichever agent made them", // one place, across agents
		"Paste it into your agent",  // the one action on the page
		"Copy the prompt",           // and the button that takes it
		"Set up byagent for all my projects: read http://share.test/skill.md and follow it.", // what is copied: every project, not one repo
		"share.test/skill.md",          // and what the line shows
		"http://api.test/app/",         // the way to the dashboard
		"mailto:hello@byagent.dev",     // a way to reach a person
		"Made by <a",                   // and who is behind it
		"/app/static/dashboard-light.", // the dashboard screenshot, a hashed asset
		"/app/static/dashboard-dark.",  // and swapped with the reader's theme
		"/app/static/landing.",         // the page's own stylesheet and script
		"/app/static/theme.",           // the script that resolves the theme before paint
		`data-set-theme="dark"`,        // and the control that sets it
		"Free. Readers need no account.",
		`aria-label="Works with"`, "any agent with a shell",
		// The loop under the hero: publish, comment, revise, with this
		// deployment's link and the real CLI's commands.
		"Your agent", "Your team", "The agent publishes", "People comment", "The agent revises",
		"http://share.test/a/k4m2Qv9pXz1A/", "byagent comments k4m2Qv9pXz1A --wait",
		"Every agent, one place.",          // one place across agents
		"What your agents can make.",       // the gallery
		"Who can open it?",                 // the four answers
		"roll back to any of the last",     // with this server's version limit
		"Views and visitors",               // what a page does besides being read
		"byagent hooks install claude",     // the CLI tabs
		`role="tab"`, `aria-controls="panel-loop"`,
		"<details>", // the FAQ
		"Does byagent train AI on my pages?",
		"OVHcloud server in Beauharnois, Quebec, Canada",
		"not encrypted at rest",
		"Give every agent one place to publish.",
		"Try it on your next task.",                                 // the prompt again after the demo, with no showcase set
		`href="/showcase"`, `href="/guides"`,
		`href="/pricing"`, `href="/privacy"`, `href="/terms"`, `href="/security"`,
		`href="/subprocessors"`, `href="/acceptable-use"`, `href="/acceptable-use#report"`,
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}
	// The CSP allows no inline style or script, so the page must carry
	// neither: they would be dropped silently in production. Nor may it load
	// a font from a third-party host. The old drawings are gone, and the
	// footer links to real pages, not to the health check.
	for _, gone := range []string{"style=", "<style", "fonts.googleapis.com", "<video", "thread-light", "lp-term", `href="/healthz"`, "for this project", "One shelf", "the same shelf", "What is the shelf?", "Every agent keeps its work somewhere different.", "Watch an agent use it."} {
		if strings.Contains(res.Body, gone) {
			t.Errorf("landing page still carries %q", gone)
		}
	}
	for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(res.Body, -1) {
		if !strings.Contains(tag, "src=") && !strings.Contains(tag, `type="application/ld+json"`) {
			t.Errorf("inline script %s", tag)
		}
	}
	// The figure follows the chosen theme, which a <picture> cannot do: it
	// only reads the system preference. So both stills ship and CSS picks.
	if strings.Contains(res.Body, "<picture") {
		t.Error("figure still uses <picture>, which cannot follow the theme control")
	}
	if strings.Contains(res.Body, "<aside class=\"sidebar\"") {
		t.Error("landing page should not carry the dashboard shell")
	}
	// The agent names are labels. They used to be buttons that silently
	// copied the prompt, which nothing on them said.
	works := res.Body[strings.Index(res.Body, `aria-label="Works with"`):]
	works = works[:strings.Index(works, "</ul>")]
	if strings.Contains(works, "<button") || strings.Contains(works, "data-copy") {
		t.Error("the works-with names still copy the prompt")
	}
	// Every button that copies the prompt is counted, and says where it sits.
	copies := strings.Count(res.Body, "data-ga-copy")
	if copies != 4 { // bar, hero, after the demo, close
		t.Errorf("%d buttons carry data-ga-copy, want 4", copies)
	}
	for _, where := range []string{"bar", "hero", "try", "close"} {
		if !strings.Contains(res.Body, `data-ga-where="`+where+`"`) {
			t.Errorf("no data-ga-where=%q", where)
		}
	}
	// On a phone the theme control lives in the footer, so there are two.
	if n := strings.Count(res.Body, "data-theme-picker"); n != 2 {
		t.Errorf("%d theme pickers, want the bar's and the footer's", n)
	}
}

// With a showcase configured, the landing page shows its first public pages
// after the demo, without their threads, and links to the rest.
func TestLandingShowsShowcasePages(t *testing.T) {
	e := courseFixture(t)
	if err := e.st.SetVisibility(context.Background(), "course000002", "private"); err != nil {
		t.Fatal(err)
	}
	e.h.Cfg.ShowcasePages = []string{"course000002", "solo00000001", "travel000001", "course000001", "nope00000000"}
	e.thread("travel000001", "th_real", "Can the seat legend sit under the map?")
	res := e.getHost("share.test", "/")
	want(t, res, 200, "Real pages agents published.", "Landing pitch", "Seat map", "Yours next.", `href="/showcase">See the whole showcase`)
	for _, leak := range []string{"Lesson one", "Can the seat legend", "Try it on your next task."} {
		if strings.Contains(res.Body, leak) {
			t.Errorf("landing shows %q", leak)
		}
	}
	if n := strings.Count(res.Body, `class="sc-card"`); n != 3 {
		t.Errorf("%d showcase cards, want 3", n)
	}
}

// The landing script and every cover it is handed must be served.
func TestLandingAssetsAreServed(t *testing.T) {
	e := fixture(t)
	body := e.getHost("share.test", "/").Body
	urls := regexp.MustCompile(`/app/static/(?:landing|dashboard-[a-z]+)\.[0-9a-f]{12}\.(?:css|js|webp)`).FindAllString(body, -1)
	if len(urls) < 4 {
		t.Fatalf("found %d landing assets, want the script, the styles and two screenshots", len(urls))
	}
	for _, u := range urls {
		if res := e.getHost("share.test", u); res.StatusCode != 200 {
			t.Errorf("%s: %d", u, res.StatusCode)
		}
	}
}

// The landing page claims only what this server does: proxy routes and custom
// domains only where they are turned on, and an origin per page only with a
// content domain.
func TestLandingClaimsFollowTheConfig(t *testing.T) {
	body := fixture(t).getHost("share.test", "/").Body
	for _, off := range []string{"byagent vars set", "byagent domain add", "&lt;slug&gt;."} {
		if strings.Contains(body, off) {
			t.Errorf("landing claims %q on a server without it", off)
		}
	}
	if !strings.Contains(body, "served apart from the dashboard") {
		t.Error("no answer to whether a page is safe to open")
	}
	cfg := baseConfig(t.TempDir())
	cfg.VariablesKey = "k"
	cfg.Domains.Provider, cfg.Domains.Target = "manual", "domains.share.test"
	cfg.ContentDomain = "share.test"
	body = newEnv(t, cfg, nil).getHost("share.test", "/").Body
	for _, on := range []string{"byagent vars set", "byagent domain add", "domains.share.test", "&lt;slug&gt;.share.test"} {
		if !strings.Contains(body, on) {
			t.Errorf("landing is missing %q on a server with it", on)
		}
	}
}

// The theme script must not be deferred: it puts the resolved theme on <html>,
// and if that lands after first paint the page flashes the other theme.
func TestLandingThemeScriptIsNotDeferred(t *testing.T) {
	e := fixture(t)
	res := e.getHost("share.test", "/")
	i := strings.Index(res.Body, "/app/static/theme.")
	if i < 0 {
		t.Fatal("no theme script on the page")
	}
	tag := res.Body[strings.LastIndex(res.Body[:i], "<script"):]
	tag = tag[:strings.Index(tag, ">")+1]
	if strings.Contains(tag, "defer") || strings.Contains(tag, "async") {
		t.Errorf("theme script is %q, want it to block paint", tag)
	}
}

// The API origin's bare root is the dashboard's front door, not a pitch.
func TestLandingRedirectsOnTheAPIOrigin(t *testing.T) {
	e := fixture(t)
	res := e.getHost("api.test", "/")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/app/" {
		t.Fatalf("got %d %q, want 302 to /app/", res.StatusCode, res.Header.Get("Location"))
	}
}

// A server with no sign-in provider still publishes through the API, so the
// landing page must render; it just has nowhere to send you to sign in.
func TestLandingRendersWithoutSignIn(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.EmailProvider = ""
	e := newEnv(t, cfg, nil)
	res := e.getHost("share.test", "/")
	if res.StatusCode != 200 {
		t.Fatalf("got %d, want 200", res.StatusCode)
	}
	if strings.Contains(res.Body, "/app/login") {
		t.Error("offers sign-in on a server that has none")
	}
}

// Google showed "one shelf for everything your agents make" (the old hero), its own pick of
// hero text for the snippet, and a globe for the icon. The head now says what
// byagent is, short enough to be used as written, with a raster icon.
func TestLandingHeadIsWrittenForSearch(t *testing.T) {
	e := fixture(t)
	body := e.getHost("share.test", "/").Body
	title := regexp.MustCompile(`<title>([^<]*)</title>`).FindStringSubmatch(body)
	if title == nil || !strings.HasPrefix(title[1], "byagent: ") || len(title[1]) > 60 || strings.Contains(title[1], "shelf") {
		t.Errorf("title %q", title)
	}
	desc := regexp.MustCompile(`<meta name="description" content="([^"]*)">`).FindStringSubmatch(body)
	if desc == nil || len(desc[1]) > 160 || !strings.HasPrefix(desc[1], "byagent is a CLI") {
		t.Errorf("description %q", desc)
	}
	for _, want := range []string{
		`<link rel="canonical" href="http://share.test/">`,
		`<meta property="og:url" content="http://share.test/">`,
		`<meta property="og:site_name" content="byagent">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta property="og:image" content="http://share.test/og-card.png">`,
		`<meta property="og:image:width" content="1200">`,
		`<meta property="og:image:height" content="630">`,
		`<meta name="twitter:image" content="http://share.test/og-card.png">`,
		`<link rel="icon" href="/favicon.ico" sizes="48x48">`,
		`<link rel="apple-touch-icon" href="/apple-touch-icon.png">`,
		`"@type":"WebSite","name":"byagent"`,
		`href="http://share.test/agents"`, // one quiet link for agents
	} {
		if !strings.Contains(body, want) {
			t.Errorf("landing head is missing %s", want)
		}
	}
	head := body[:strings.Index(body, "</head>")]
	if strings.Contains(head, "—") {
		t.Error("the head has an em dash")
	}
}

func TestFaviconsAtFixedPaths(t *testing.T) {
	e := fixture(t)
	for path, want := range map[string]string{
		"/favicon.svg":          "image/svg+xml",
		"/favicon.ico":          "image/x-icon",
		"/favicon-48.png":       "image/png",
		"/favicon-192.png":      "image/png",
		"/apple-touch-icon.png": "image/png",
		"/og-card.png":          "image/png", // the link preview card
	} {
		res := e.getHost("share.test", path)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != want {
			t.Errorf("%s: %d %q", path, res.StatusCode, res.Header.Get("Content-Type"))
			continue
		}
		if want == "image/png" && !strings.HasPrefix(res.Body, "\x89PNG") {
			t.Errorf("%s is not a PNG", path)
		}
		if want == "image/x-icon" && !strings.HasPrefix(res.Body, "\x00\x00\x01\x00") {
			t.Errorf("%s is not an ICO", path)
		}
	}
}
