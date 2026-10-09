package app

import (
	"bytes"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/config"
)

// siteEnv is a server with the managed limits, so the pricing page and the
// policies print the numbers byagent.dev actually runs with.
func siteEnv(t *testing.T) *env {
	t.Helper()
	cfg := baseConfig(t.TempDir())
	cfg.Limits = config.Limits{VersionBytes: 26214400, Files: 500, VersionsKept: 20, PublishPerMin: 10,
		ArtifactsPerTenant: 200, CommentBytes: 2048, CommentsPerMin: 20, AnchorBytes: 4096}
	return newEnv(t, cfg, nil)
}

// sitePages is every public page next to the landing page, with phrases each
// one must carry: what a careful reader came to the page to find out.
var sitePages = map[string][]string{
	"/about": {
		"About byagent", "What is byagent?", "Who makes byagent?", "How is it built?", "What does byagent promise?",
		"Anup Aglawe", "September 2026", `href="https://github.com/anup-a"`, `href="https://x.com/anup_x_dev"`,
		`"@type":"AboutPage"`, `href="/security"`, `href="/privacy"`, "hello@byagent.dev",
	},
	"/privacy": {
		"Privacy policy", "Who we are", "Two roles", "What we collect and why", "Cookies",
		"International transfers", "Retention and deletion", "Your rights", "Children", "Contact",
		"byagent is operated by Anup Aglawe", "Controller for account and site data", "Processor for page content and reader comments",
		"Legal basis", "6(1)(b)", "6(1)(f)",
		"We do not store reader IP addresses", "how many different visitors", "changes every UTC day and is never written down",
		"Google Analytics", "a banner asks first", "Cookie settings", "byagent_consent", "Consent, 6(1)(a)", "does not run on published pages",
		"cdnjs.cloudflare.com", "cdn.jsdelivr.net", "Google Fonts",
		"artifacts_session", "artifact_unlock_", "_ga",
		"Singapore", "standard contractual clauses",
		"needs no email", "Guest pages", "within 30 days", "for 5 days", "Up to 20 versions per page",
		"access", "correction", "deletion", "restriction", "portability", "objection", "withdraw consent", "data protection authority",
		"not for anyone under 16", "We do not sell personal data", "to train AI models",
		"Privacy request", `href="/subprocessors"`, `href="/security"`,
	},
	"/terms": {
		"Terms of service", "The service", "Accounts and keys", "Your content", "Readers and comments",
		"Private pages and share codes", "Acceptable use", "Free service, limits and changes", "Suspension and termination",
		"Data processing", "Disclaimers", "Limitation of liability", "Governing law", "Changes", "Contact",
		"You own what you publish", "only as needed to provide byagent", "We do not use your content to train AI models",
		"visible to anyone who can see the page", "moderate the comments", "cannot prevent that",
		"only on your instructions", "keep it confidential", "help you answer requests", "delete it when you ask",
		"within 72 hours", "laws of India", "at least 30 days' notice", `href="/acceptable-use"`,
	},
	"/security": {
		"Security", "Hosting and data location", "Encryption", "Origins and isolation", "Access to published pages",
		"Accounts and keys", "Who has access to production", "Backups", "Incident response", "Reporting a vulnerability",
		"What we do not claim",
		"one machine at Fly.io", "Singapore region", "encrypted at rest", "uses TLS",
		"/a/&lt;id&gt;/", "app.byagent.dev", "CSP sandbox", "Content Security Policy",
		"after 10 wrong codes", "until 15 minutes pass", "for 14 days",
		"stored as SHA-256 hashes", "shown once", "expires after 15 minutes",
		"The operator only", "keeps each snapshot for 5 days",
		"without undue delay and within 72 hours", "Security report", "Good-faith research is welcome",
		"No SOC 2", "One region and one machine", "shares the byagent.dev origin with every other public page",
	},
	"/subprocessors": {
		"Subprocessors", "Last updated 8 October 2026",
		"Fly.io", "Hosting: compute and storage", "Singapore region",
		"Resend", "Delivering sign-in emails",
		"GitHub", "only if you choose GitHub",
		"Google", "Google Analytics",
		"United States", "Before we add a subprocessor",
	},
	"/acceptable-use": {
		"Acceptable use policy", "Malware", "Phishing or credential harvesting", "Illegal content",
		"Child sexual abuse material", "Harassment", "Spam", "Impersonation", "infringes",
		"Get around the service's limits", "Attack, probe or overload", "Create accounts in bulk",
		"How we enforce it", "remove a page", "turn off its comments", "suspend",
		`id="report"`, "Report abuse", "Abuse report", "the page URL",
	},
	"/pricing": {
		"Free. No card.", "There is no paid plan",
		"25 MB per published version", "500 files per version", "20 versions kept per page",
		"200 pages per workspace", "10 publishes a minute per key", "Comments up to 2 KB each",
		"What readers need", "Nothing.", "Will it stay free?", "at least 30 days' notice",
		"Need more?", "mailto:hello@byagent.dev", "http://share.test/pricing.md",
	},
}

func TestSitePagesServe(t *testing.T) {
	e := siteEnv(t)
	for path, phrases := range sitePages {
		res := e.getHost("share.test", path)
		if res.StatusCode != 200 {
			t.Errorf("%s: got %d, want 200", path, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: content type %q", path, ct)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=300" {
			t.Errorf("%s: cache control %q", path, cc)
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("%s: no CSP, got %q", path, csp)
		}
		for _, want := range phrases {
			if !strings.Contains(res.Body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
		// Every page carries the site bar and footer, and its own canonical.
		for _, want := range []string{
			`<link rel="canonical" href="http://share.test` + path + `">`,
			`data-set-theme="dark"`, "/app/static/theme.",
			`href="/privacy"`, `href="/terms"`, `href="/acceptable-use#report"`, "mailto:hello@byagent.dev",
		} {
			if !strings.Contains(res.Body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
		if strings.Contains(res.Body, "<aside class=\"sidebar\"") {
			t.Errorf("%s: carries the dashboard shell", path)
		}
		if got := inlineScripts(res.Body); len(got) > 0 {
			t.Errorf("%s: inline script the CSP will block: %q", path, got)
		}
	}
}

// Policies are versioned: a reader can tell which text they agreed to.
func TestPoliciesCarryDateVersionAndSummary(t *testing.T) {
	e := siteEnv(t)
	for _, path := range []string{"/privacy", "/terms", "/security", "/subprocessors", "/acceptable-use"} {
		body := e.getHost("share.test", path).Body
		for _, want := range []string{"Effective 8 October 2026", "Version v1.6", `class="lp-summary"`, "In short"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
		title := regexp.MustCompile(`<title>([^<]*)</title>`).FindStringSubmatch(body)
		if title == nil || !strings.HasSuffix(title[1], " · byagent") || len(title[1]) > 60 {
			t.Errorf("%s: title %q", path, title)
		}
		desc := regexp.MustCompile(`<meta name="description" content="([^"]*)">`).FindStringSubmatch(body)
		if desc == nil || len(desc[1]) > 160 {
			t.Errorf("%s: description %q", path, desc)
		}
	}
}

// The public copy rules, for the landing page and every page next to it: no
// em dashes, and the product is the CLI, so MCP is never mentioned.
func TestSiteCopyRules(t *testing.T) {
	e := siteEnv(t)
	paths := []string{"/"}
	for p := range sitePages {
		paths = append(paths, p)
	}
	for _, path := range paths {
		body := e.getHost("share.test", path).Body
		if strings.Contains(body, "—") {
			t.Errorf("%s: has an em dash", path)
		}
		if strings.Contains(strings.ToLower(body), "mcp") {
			t.Errorf("%s: mentions MCP; the public interface is the CLI", path)
		}
		if strings.Contains(body, "self-host") || strings.Contains(body, "self host") {
			t.Errorf("%s: mentions self-hosting", path)
		}
		if strings.Contains(body, "<no value>") || strings.Contains(body, "{{") {
			t.Errorf("%s: unrendered template", path)
		}
	}
}

// Google Analytics follows the same rule as the landing page: it is on when
// configured, and the privacy policy is what says so.
func TestSitePagesAnalytics(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.GoogleAnalyticsID = "G-TEST123"
	e := newEnv(t, cfg, nil)
	body := e.getHost("share.test", "/privacy").Body
	if !strings.Contains(body, `data-ga-id="G-TEST123"`) {
		t.Error("the privacy page says GA runs on the site pages, but it is not loaded there")
	}
}

// The link preview card is a real 1200 x 630 PNG.
func TestCardIs1200x630(t *testing.T) {
	e := fixture(t)
	res := e.getHost("share.test", "/og-card.png")
	if res.StatusCode != 200 {
		t.Fatalf("got %d", res.StatusCode)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader([]byte(res.Body)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 1200 || cfg.Height != 630 {
		t.Errorf("card is %dx%d, want 1200x630", cfg.Width, cfg.Height)
	}
}
