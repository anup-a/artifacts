package app

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/config"
)

var ldJSON = regexp.MustCompile(`<script type="application/ld\+json">([^<]*)</script>`)

// The questions on the page and in the FAQPage data block come from one list,
// each question is a heading an answer engine can quote, and the limits in the
// answers are this server's, not copied numbers.
func TestLandingFAQIsMarkedUp(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.Limits = config.Limits{VersionBytes: 25 << 20, ArtifactsPerTenant: 200, VersionsKept: 20}
	e := newEnv(t, cfg, nil)
	body := e.getHost("share.test", "/").Body
	var faq struct {
		Type string `json:"@type"`
		Main []struct {
			Name   string `json:"name"`
			Answer struct {
				Text string `json:"text"`
			} `json:"acceptedAnswer"`
		} `json:"mainEntity"`
	}
	for _, m := range ldJSON.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], `"FAQPage"`) {
			if err := json.Unmarshal([]byte(m[1]), &faq); err != nil {
				t.Fatal(err)
			}
		}
	}
	if faq.Type != "FAQPage" || len(faq.Main) != len(landingFAQ(cfg.Limits)) {
		t.Fatalf("FAQPage block: %+v", faq)
	}
	for _, q := range faq.Main {
		if !strings.Contains(body, "<summary><h3>"+html.EscapeString(q.Name)+"</h3></summary>") {
			t.Errorf("question %q is not a heading on the page", q.Name)
		}
		if q.Answer.Text == "" || strings.ContainsAny(q.Answer.Text, "<>") {
			t.Errorf("answer to %q: %q", q.Name, q.Answer.Text)
		}
	}
	for _, want := range []string{"up to 200 pages of up to 25 MB", "The last 20 versions are kept"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing is missing %s", want)
		}
	}
	// The site and the app name one Organization as publisher, by @id.
	var graph struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(ldJSON.FindStringSubmatch(body)[1]), &graph); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, n := range graph.Graph {
		if n["@type"] == "Organization" {
			ids["org"], _ = n["@id"].(string)
			f, _ := n["founder"].(map[string]any)
			ids["founder"], _ = f["@id"].(string)
			continue
		}
		// The founder's own profiles, so the Organization links to a person.
		if n["@type"] == "Person" {
			ids["person"], _ = n["@id"].(string)
			if s := fmt.Sprint(n["sameAs"]); s != "[https://github.com/anup-a https://x.com/anup_x_dev]" {
				t.Errorf("founder sameAs %s", s)
			}
			continue
		}
		p, _ := n["publisher"].(map[string]any)
		ids[n["@type"].(string)], _ = p["@id"].(string)
	}
	if ids["org"] != "http://share.test/#org" || ids["WebSite"] != ids["org"] || ids["SoftwareApplication"] != ids["org"] {
		t.Errorf("publisher ids %v", ids)
	}
	if ids["founder"] != "http://share.test/#founder" || ids["person"] != ids["founder"] {
		t.Errorf("founder ids %v", ids)
	}
	if strings.Contains(body, "—") || strings.Contains(body, "MCP") {
		t.Error("the landing page has an em dash or mentions MCP")
	}
}

// An agent that asks the root for Markdown is sent to the agents page; a
// browser still gets the landing page, and a cache keeps the two apart.
func TestLandingMarkdownForAgents(t *testing.T) {
	e := fixture(t)
	for accept, want := range map[string]int{
		"text/markdown":                             http.StatusSeeOther,
		"text/markdown, text/html;q=0.9":            http.StatusSeeOther,
		"text/html,application/xhtml+xml,*/*;q=0.8": http.StatusOK,
		"": http.StatusOK,
	} {
		req, _ := http.NewRequest("GET", e.srv.URL+"/", nil)
		req.Host = "share.test"
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		res, err := e.cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != want || res.Header.Get("Vary") != "Accept" {
			t.Errorf("Accept %q: %d, Vary %q", accept, res.StatusCode, res.Header.Get("Vary"))
		}
		if want == http.StatusSeeOther && res.Header.Get("Location") != "/agents.md" {
			t.Errorf("Accept %q: Location %q", accept, res.Header.Get("Location"))
		}
	}
}

func TestSkipLinkIsFirst(t *testing.T) {
	body := fixture(t).getHost("share.test", "/").Body
	after := body[strings.Index(body, "<body>"):]
	first := regexp.MustCompile(`<a [^>]*>`).FindString(after)
	if first != `<a class="skip-link" href="#main">` || !strings.Contains(body, `id="main"`) {
		t.Errorf("first link %q", first)
	}
}
