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
		"npx byagent publish",    // the command that is the whole product
		"http://api.test/app/",   // the way to the dashboard
		"http://api.test",        // what you point the CLI at
		"share.test/skill.md",    // what you paste to an agent
		"share.test/a/",          // what a published link looks like
		"mcpServers",             // how an agent reads the shelf back
		"artifact_comments",      // and the tool that closes the loop
		"<video",                 // the dashboard recording carries the product
		"/app/static/dashboard.", // served as a hashed asset
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}
	if strings.Contains(res.Body, "<aside class=\"sidebar\"") {
		t.Error("landing page should not carry the dashboard shell")
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
