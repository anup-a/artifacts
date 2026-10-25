package app

import (
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/agents"
)

// A guide is dressed as a page of the site: the bar, the stylesheet and the
// footer around the body the agents package rendered.
func TestGuidePageUsesTheSiteLayout(t *testing.T) {
	e := siteEnv(t)
	g := agents.Guide{Slug: "codex", Title: "Publish Codex output as a shareable link", Description: "Give Codex a link to hand back.", Updated: "2026-10-04"}
	doc := agents.GuideDoc{Guide: g, Guides: []agents.Guide{g, {Slug: "cursor", Title: "Cursor guide"}}, Path: "/guides/codex", MDPath: "/guides/codex.md",
		Body: template.HTML("<h2 id=\"install\">Install</h2><pre><code>npx byagent</code></pre>"), LD: template.JS(`{"@context":"https://schema.org"}`)}
	rec := httptest.NewRecorder()
	e.h.GuidePage(rec, httptest.NewRequest("GET", "http://share.test/guides/codex", nil), doc)
	body := rec.Body.String()
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=300" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("got %d, headers %v", rec.Code, rec.Header())
	}
	for _, want := range []string{
		`class="lp-bar"`, `class="lp-foot"`, `rel="stylesheet"`,
		"<title>Publish Codex output as a shareable link · byagent</title>",
		`<link rel="canonical" href="http://share.test/guides/codex">`,
		`<link rel="alternate" type="text/markdown" href="http://share.test/guides/codex.md"`,
		`<script type="application/ld+json">{"@context":"https://schema.org"}</script>`,
		"<h1>Publish Codex output as a shareable link</h1>",
		`<h2 id="install">Install</h2><pre><code>npx byagent</code></pre>`,
		`href="/guides/cursor"`,
		`href="/guides" aria-current="page">Guides</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `href="/guides/codex"><span`) {
		t.Error("a guide lists itself under More guides")
	}

	rec = httptest.NewRecorder()
	e.h.GuidePage(rec, httptest.NewRequest("GET", "http://share.test/guides", nil), agents.GuideDoc{Index: true, Guides: doc.Guides, Path: "/guides", MDPath: "/guides.md"})
	if body := rec.Body.String(); !strings.Contains(body, "Share what your agents write") || !strings.Contains(body, `href="/guides/codex"`) || !strings.Contains(body, "Give Codex a link to hand back.") {
		t.Errorf("index: %d %.300s", rec.Code, body)
	}
}
