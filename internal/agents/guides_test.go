package agents

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestGuidePages(t *testing.T) {
	get := server(t)
	ld := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	for _, g := range guides {
		md := get("/guides/"+g.Slug+".md", "")
		if md.status != 200 || !strings.HasPrefix(md.header.Get("Content-Type"), "text/markdown") || !strings.HasPrefix(md.body, "# "+g.Title+"\n") {
			t.Errorf("%s.md: got %d %q, %.60q", g.Slug, md.status, md.header.Get("Content-Type"), md.body)
		}
		if !strings.Contains(md.body, "byagent ") || !strings.Contains(md.body, "https://share.test/") {
			t.Errorf("%s.md: no command or no link back to the site", g.Slug)
		}

		page := get("/guides/"+g.Slug, "")
		if page.status != 200 || !strings.HasPrefix(page.header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: got %d %q", g.Slug, page.status, page.header.Get("Content-Type"))
			continue
		}
		for _, want := range []string{
			`<link rel="canonical" href="https://share.test/guides/` + g.Slug + `">`,
			`<link rel="alternate" type="text/markdown" href="https://share.test/guides/` + g.Slug + `.md">`,
			"<h1>",
			"<pre><code",
		} {
			if !strings.Contains(page.body, want) {
				t.Errorf("%s: missing %q", g.Slug, want)
			}
		}
		if len(g.Description) > 160 {
			t.Errorf("%s: description is %d characters; search results cut it at about 160", g.Slug, len(g.Description))
		}
		m := ld.FindStringSubmatch(page.body)
		if m == nil {
			t.Errorf("%s: no JSON-LD", g.Slug)
			continue
		}
		var v struct {
			Graph []map[string]any `json:"@graph"`
		}
		if err := json.Unmarshal([]byte(m[1]), &v); err != nil || len(v.Graph) != 2 || v.Graph[0]["headline"] != g.Title {
			t.Errorf("%s: JSON-LD %v %s", g.Slug, err, m[1])
		}
		if res := get("/guides/"+g.Slug, "text/markdown"); !strings.HasPrefix(res.header.Get("Content-Type"), "text/markdown") {
			t.Errorf("%s: Accept: text/markdown got %q", g.Slug, res.header.Get("Content-Type"))
		}
	}
	index := get("/guides", "")
	for _, g := range guides {
		if !strings.Contains(index.body, `href="https://share.test/guides/`+g.Slug+`"`) {
			t.Errorf("/guides does not link %s", g.Slug)
		}
	}
	for _, path := range []string{"/guides/nope", "/guides/nope.md", "/guides/codex.html"} {
		if res := get(path, ""); res.status != 404 {
			t.Errorf("%s: got %d, want 404", path, res.status)
		}
	}
	if res := get("/guides/", ""); res.status != 301 || res.header.Get("Location") != "/guides" {
		t.Errorf("/guides/ got %d to %q", res.status, res.header.Get("Location"))
	}
	if res := get("/llms.txt", ""); !strings.Contains(res.body, "## Guides") || !strings.Contains(res.body, "(https://share.test/guides/codex.md)") {
		t.Error("llms.txt does not list the guides")
	}
	if res := get("/"+indexNowKey+".txt", ""); res.status != 200 || res.body != indexNowKey {
		t.Errorf("IndexNow key file: %d %q", res.status, res.body)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	got := markdownToHTML("# A <b> title\n\nOne `x<y` and **bold** and [link](https://e.test/?a=1&b=2).\nSame para. See https://e.test/x.\n\n- one\n- two\n\n1. first\n2. second\n\n> quoted\n\n| a | b |\n|---|---|\n| `c` | d |\n\n```sh\necho <hi>\n```\n")
	for _, want := range []string{
		`<h1 id="a-b-title">A &lt;b&gt; title</h1>`,
		`<p>One <code>x&lt;y</code> and <strong>bold</strong> and <a href="https://e.test/?a=1&amp;b=2">link</a>. Same para. See <a href="https://e.test/x">https://e.test/x</a>.</p>`,
		"<ul>\n<li>one</li>\n<li>two</li>\n</ul>",
		"<ol>\n<li>first</li>\n<li>second</li>\n</ol>",
		"<blockquote><p>quoted</p></blockquote>",
		"<thead><tr><th>a</th><th>b</th></tr></thead><tbody>",
		"<tr><td><code>c</code></td><td>d</td></tr>",
		`<pre><code class="language-sh">echo &lt;hi&gt;</code></pre>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "---") {
		t.Error("the table separator row was rendered")
	}
}
