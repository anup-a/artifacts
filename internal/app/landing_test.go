package app

import (
	"io"
	"net/http"
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
		"mailto:team@creatica.app",     // a way to reach a person
		"Made by <a",                   // and who is behind it
		"/app/static/dashboard-light.", // the dashboard screenshot, a hashed asset
		"/app/static/dashboard-dark.",  // and swapped with the reader's theme
		"/app/static/thread-light.",    // the comment thread inset over it
		"/app/static/thread-dark.",
		"/app/static/theme.",    // the script that resolves the theme before paint
		`data-set-theme="dark"`, // and the control that sets it
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}
	// The page is a pitch, not the manual: a still instead of a recording, and
	// no JSON sample or drawn mock of the shelf the screenshot already shows.
	// It speaks to the agent, so the terminal walkthrough for people is gone.
	for _, gone := range []string{"<video", "lp-two-up", "lp-shelf", `"artifacts"`, "For humans", "npm install -g"} {
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
