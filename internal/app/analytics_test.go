package app

import (
	"regexp"
	"strings"
	"testing"
)

// inlineScript matches a <script> tag with no src: the CSP has no
// 'unsafe-inline', so a browser refuses to run one.
var inlineScript = regexp.MustCompile(`<script(?:\s[^>]*)?>`)

func inlineScripts(body string) []string {
	var found []string
	for _, tag := range inlineScript.FindAllString(body, -1) {
		if !strings.Contains(tag, " src=") {
			found = append(found, tag)
		}
	}
	return found
}

// GA4 used to start from an inline snippet that the CSP blocked, so the tag
// was on every page and no hit was ever sent. It must start from a file.
func TestAnalyticsStartsFromAFile(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.GoogleAnalyticsID = "G-TEST123"
	e := newEnv(t, cfg, nil)
	for _, path := range []string{"/", "/app/login"} {
		res := e.getHost("share.test", path)
		if path != "/" {
			res = e.getHost("api.test", path)
		}
		if res.StatusCode != 200 {
			t.Fatalf("%s: got %d", path, res.StatusCode)
		}
		if !regexp.MustCompile(`<script src="/app/static/ga\.[0-9a-f]+\.js" data-ga-id="G-TEST123"></script>`).MatchString(res.Body) {
			t.Errorf("%s: no ga.js tag carrying the measurement ID", path)
		}
		if !strings.Contains(res.Body, "googletagmanager.com/gtag/js?id=G-TEST123") {
			t.Errorf("%s: gtag.js is not loaded", path)
		}
		if got := inlineScripts(res.Body); len(got) > 0 {
			t.Errorf("%s: inline script the CSP will block: %q", path, got)
		}
		if csp := res.Header.Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s: CSP was loosened instead: %q", path, csp)
		}
	}
}

func TestAnalyticsScriptIsServed(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.GoogleAnalyticsID = "G-TEST123"
	e := newEnv(t, cfg, nil)
	body := e.getHost("share.test", "/").Body
	src := regexp.MustCompile(`/app/static/ga\.[0-9a-f]+\.js`).FindString(body)
	if src == "" {
		t.Fatal("no ga.js on the page")
	}
	res := e.getHost("share.test", src)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("ga.js: got %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{"data-ga-id", `gtag("config", id)`} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("ga.js is missing %q", want)
		}
	}
}

func TestNoAnalyticsWithoutAnID(t *testing.T) {
	e := fixture(t)
	body := e.getHost("share.test", "/").Body
	if strings.Contains(body, "googletagmanager") || strings.Contains(body, "data-ga-id") {
		t.Error("analytics loaded with no measurement ID configured")
	}
}
