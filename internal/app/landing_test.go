package app

import (
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
		"Paste it into your agent",     // the one action on the page
		"Copy the prompt",              // and the button that takes it
		"http://api.test/app/",         // the way to the dashboard
		"http://api.test/app/keys",     // where the key the agent asks for is made
		"share.test/skill.md",          // what you paste to an agent
		"share.test/a/",                // what a published link looks like
		"The next run reads it back",   // the loop: comments come back into the agent
		"searchable by every agent",    // the chart: the workspace is a library for agents too
		"its own memory",               // and it sits next to what the agent remembers
		"into the next run",            // and the thread flows back into the agent
		"mailto:hello@byagent.dev",     // a way to reach a person
		"Made by <a",                   // and who is behind it
		"/app/static/dashboard-light.", // the dashboard screenshot, a hashed asset
		"/app/static/dashboard-dark.",  // and swapped with the reader's theme
		"/app/static/thread-light.",    // the comment thread inset over it
		"/app/static/thread-dark.",
		"/app/static/theme.",                // the script that resolves the theme before paint
		`data-set-theme="dark"`,             // and the control that sets it
		"One shelf for everything",          // the positioning
		"Anyone comments. Any agent reads.", // and its subhead
		"Free. Readers need no account.",    // the microcopy under the action
		`aria-label="Works with"`,           // the agents it works with, as names
		"any agent with a shell",
		"plan_v3_final.md",                    // before: the scattered files
		">byagent comments<", "--open --json", // the agent reads comments as JSON
		`"anchor_status"`, `"author_kind"`, // in the real shape the API returns
		"byagent resolve k4m2Qv9pXz1A",  // and closes the thread
		"byagent list --project Launch", // the shelf, searched
		"byagent collections",
		"BRIEF.md",            // the handoff note per project
		"six-digit code",      // private pages
		"byagent portal-link", // client portals
		"lasts seven days",    // and how long a portal link lives
		"<details>",           // the FAQ
		"Does byagent train AI on my pages?",
		"Fly.io in its Singapore region",
		`href="/pricing"`, `href="/privacy"`, `href="/terms"`, `href="/security"`,
		`href="/subprocessors"`, `href="/acceptable-use"`, `href="/acceptable-use#report"`,
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}
	// The page is a pitch, not the manual: a still instead of a recording, and
	// no drawn mock of the shelf the screenshot already shows. It speaks to the
	// agent, so the terminal walkthrough for people is gone. The footer links
	// to real pages, not to the health check.
	for _, gone := range []string{"<video", "lp-two-up", "lp-shelf", `"artifacts"`, "For humans", "npm install -g", `href="/healthz"`} {
		if strings.Contains(res.Body, gone) {
			t.Errorf("landing page still carries %q", gone)
		}
	}
	// The figure follows the chosen theme, which a <picture> cannot do: it
	// only reads the system preference. So both stills ship and CSS picks.
	if strings.Contains(res.Body, "<picture") {
		t.Error("figure still uses <picture>, which cannot follow the theme control")
	}
	if n := strings.Count(res.Body, "lp-shot-main"); n != 2 {
		t.Errorf("got %d shelf images, want one per theme", n)
	}
	if n := strings.Count(res.Body, "lp-shot-inset"); n != 2 {
		t.Errorf("got %d thread images, want one per theme", n)
	}
	if strings.Contains(res.Body, "<aside class=\"sidebar\"") {
		t.Error("landing page should not carry the dashboard shell")
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

// Google showed "one shelf for everything your agents make", its own pick of
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
