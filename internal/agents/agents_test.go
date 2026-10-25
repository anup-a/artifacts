package agents

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/config"
)

type reply struct {
	status int
	header http.Header
	body   string
}

func server(t *testing.T) func(path, accept string) reply {
	t.Helper()
	mux := http.NewServeMux()
	(&Handler{Cfg: config.Config{
		ShareOrigin: "https://share.test",
		APIOrigin:   "https://api.test",
		Limits:      config.Limits{VersionBytes: 26214400, Files: 500, VersionsKept: 20, PublishPerMin: 10, ArtifactsPerTenant: 200, CommentBytes: 2048, CommentsPerMin: 20},
	}}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(path, accept string) reply {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		res, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return reply{res.StatusCode, res.Header, string(b)}
	}
}

// publicPaths is every page this package serves, the ones the sitemap and
// llms.txt point crawlers at.
func publicPaths() []string {
	out := []string{"/agents", "/agents.md", "/agents/facts.json", "/pricing.md", "/llms.txt", "/llms-full.txt", "/robots.txt", "/sitemap.xml"}
	for _, t := range tasks {
		out = append(out, "/agents/tasks/"+t.Slug+".md")
	}
	out = append(out, "/guides", "/guides.md")
	for _, g := range guides {
		out = append(out, "/guides/"+g.Slug, "/guides/"+g.Slug+".md")
	}
	return out
}

func TestRoutesServeTheRightTypes(t *testing.T) {
	get := server(t)
	for path, want := range map[string][]string{
		"/agents":            {"text/html", "byagent for agents", "https://share.test/agents/tasks/publish.md", "https://api.test/app/keys", "npm install -g byagent", "curl -s https://share.test/agents.md"},
		"/agents.md":         {"text/markdown", "# byagent for agents", "## Entrypoints", "## Tasks", "## Scope", "does not", "https://share.test/skill.md"},
		"/agents/facts.json": {"application/json", `"name": "byagent"`},
		"/llms.txt":          {"text/plain", "# byagent\n", "\n> ", "## Start here", "[Skill](https://share.test/skill.md)", "(https://share.test/agents/tasks/brief.md)", "## Legal", "[Privacy](https://share.test/privacy)", "[Terms](https://share.test/terms)", "[Security](https://share.test/security)", "[Subprocessors](https://share.test/subprocessors)", "[Acceptable use](https://share.test/acceptable-use)", "[Pricing](https://share.test/pricing.md)"},
		"/pricing.md":        {"text/markdown", "# byagent pricing", "no paid plan", "- 25 MB per published version", "- 500 files per version", "- 20 versions kept per page", "- 200 pages per workspace", "- 10 publishes a minute per key", "Comments up to 2 KB each", "30 days' notice", "hello@byagent.dev", "https://share.test/pricing"},
		"/llms-full.txt":     {"text/plain", "# byagent for agents", "# Publish a page", "# Start from a project brief", "https://share.test/skill.md"},
		"/robots.txt":        {"text/plain", "User-agent: *", "Allow: /", "Disallow: /api/", "Sitemap: https://share.test/sitemap.xml"},
		"/sitemap.xml":       {"application/xml", "<loc>https://share.test/</loc>", "<loc>https://share.test/agents</loc>"},
	} {
		res := get(path, "")
		if res.status != 200 {
			t.Errorf("%s: got %d", path, res.status)
			continue
		}
		if ct := res.header.Get("Content-Type"); !strings.HasPrefix(ct, want[0]) {
			t.Errorf("%s: content type %q, want %s", path, ct, want[0])
		}
		for _, phrase := range want[1:] {
			if !strings.Contains(res.body, phrase) {
				t.Errorf("%s: missing %q", path, phrase)
			}
		}
		if res.header.Get("Cache-Control") != "public, max-age=300" {
			t.Errorf("%s: cache control %q", path, res.header.Get("Cache-Control"))
		}
	}
}

func TestAgentsNegotiatesMarkdown(t *testing.T) {
	get := server(t)
	res := get("/agents", "text/markdown, text/html;q=0.9")
	if !strings.HasPrefix(res.header.Get("Content-Type"), "text/markdown") || !strings.HasPrefix(res.body, "# byagent for agents") {
		t.Fatalf("Accept: text/markdown got %q", res.header.Get("Content-Type"))
	}
	if res.header.Get("Vary") != "Accept" {
		t.Error("no Vary: Accept, so a cache could serve one variant for the other")
	}
	res = get("/agents", "text/html,application/xhtml+xml,*/*;q=0.8")
	if !strings.HasPrefix(res.header.Get("Content-Type"), "text/html") {
		t.Fatalf("a browser got %q", res.header.Get("Content-Type"))
	}
	if !strings.Contains(res.header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("the HTML page has no locked-down CSP")
	}
	if res := get("/agents/", ""); res.status != 301 || res.header.Get("Location") != "/agents" {
		t.Errorf("/agents/ got %d to %q", res.status, res.header.Get("Location"))
	}
}

func TestTaskPages(t *testing.T) {
	get := server(t)
	for _, task := range tasks {
		res := get("/agents/tasks/"+task.Slug+".md", "")
		if res.status != 200 || !strings.HasPrefix(res.body, "# "+task.Title+"\n") {
			t.Errorf("%s: got %d, %.60q", task.Slug, res.status, res.body)
		}
		if !strings.Contains(res.body, "https://share.test/agents") {
			t.Errorf("%s: no way back to /agents", task.Slug)
		}
		if !strings.Contains(res.body, "byagent ") {
			t.Errorf("%s: no CLI command", task.Slug)
		}
	}
	for _, path := range []string{"/agents/tasks/nope.md", "/agents/tasks/publish", "/agents/tasks/publish.html"} {
		if res := get(path, ""); res.status != 404 {
			t.Errorf("%s: got %d, want 404", path, res.status)
		}
	}
}

func TestFactsAreCLIOnlyAndCarryTheServerLimits(t *testing.T) {
	get := server(t)
	var f struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Interface struct {
			Kind, Install string
		} `json:"interface"`
		Auth struct {
			Publish    string `json:"publish"`
			KeyOverAPI string `json:"key_over_api"`
		} `json:"auth"`
		Limits struct {
			Files int `json:"files_per_version"`
		} `json:"limits"`
		Tasks []struct{ Doc string } `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(get("/agents/facts.json", "").body), &f); err != nil {
		t.Fatal(err)
	}
	if f.Name != "byagent" || f.URL != "https://share.test/" || f.Interface.Kind != "cli" || f.Interface.Install != "npm install -g byagent" {
		t.Errorf("identity: %+v", f)
	}
	if !strings.Contains(f.Auth.Publish, "https://api.test/app/connect") || !strings.Contains(f.Auth.Publish, "https://api.test/app/keys") ||
		!strings.Contains(f.Auth.KeyOverAPI, "signed-in person") {
		t.Errorf("auth: %+v", f.Auth)
	}
	if f.Limits.Files != 500 {
		t.Errorf("limits come from config, got %d files", f.Limits.Files)
	}
	if len(f.Tasks) != len(tasks) || f.Tasks[0].Doc != "https://share.test/agents/tasks/setup.md" {
		t.Errorf("tasks: %+v", f.Tasks)
	}
}

func TestSitemapListsEveryPublicPage(t *testing.T) {
	get := server(t)
	var set struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal([]byte(get("/sitemap.xml", "").body), &set); err != nil {
		t.Fatalf("sitemap is not XML: %v", err)
	}
	listed := map[string]bool{}
	for _, u := range set.URLs {
		listed[u.Loc] = true
	}
	// The site pages internal/app serves on the share origin are listed too.
	site := []string{"/", "/skill.md", "/showcase", "/pricing", "/about", "/privacy", "/terms", "/security", "/subprocessors", "/acceptable-use"}
	for _, p := range append(publicPaths(), site...) {
		if p == "/robots.txt" || p == "/sitemap.xml" {
			continue
		}
		// A Markdown copy of an HTML page is not listed: the page is.
		if listed["https://share.test"+p] == (markdownTwins()[p] != "") {
			t.Errorf("sitemap lists %s: %v", p, listed["https://share.test"+p])
		}
	}
}

// markdownTwins maps each Markdown copy of an HTML page to that page.
func markdownTwins() map[string]string {
	out := map[string]string{"/agents.md": "/agents", "/pricing.md": "/pricing", "/guides.md": "/guides"}
	for _, g := range guides {
		out["/guides/"+g.Slug+".md"] = "/guides/" + g.Slug
	}
	return out
}

// The Markdown copies name their HTML page as canonical, so search engines
// index the page and not both.
func TestMarkdownTwinsNameTheirPageAsCanonical(t *testing.T) {
	get := server(t)
	for md, page := range markdownTwins() {
		want := `<https://share.test` + page + `>; rel="canonical"`
		if got := get(md, "").header.Get("Link"); got != want {
			t.Errorf("%s: Link %q, want %q", md, got, want)
		}
	}
}

// The public copy rules: the product is the CLI, and no em dashes.
func TestCopyRules(t *testing.T) {
	get := server(t)
	for _, path := range publicPaths() {
		body := get(path, "").body
		if strings.Contains(body, "—") {
			t.Errorf("%s: has an em dash", path)
		}
		if strings.Contains(strings.ToLower(body), "mcp") {
			t.Errorf("%s: mentions MCP; the public interface is the CLI", path)
		}
		if strings.Contains(body, "{{") || strings.Contains(body, "<no value>") {
			t.Errorf("%s: unrendered template", path)
		}
	}
}
