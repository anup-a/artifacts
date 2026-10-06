package app

import (
	"context"
	"strings"
	"testing"
)

// With page hosts on, every link the site makes to a page names the page's
// own host, the CSP lets the dashboard frame and show it, and the policy
// pages describe one origin per page.
func TestSiteLinksToPageHosts(t *testing.T) {
	e := courseFixture(t)
	e.h.Cfg.ContentScheme, e.h.Cfg.ContentDomain = "https", "page.test"
	a, err := e.st.Artifact(context.Background(), "course000001")
	if err != nil || a.Slug == "" {
		t.Fatalf("%v %+v", err, a)
	}
	own := "https://" + a.Slug + ".page.test/"
	e.h.Cfg.ShowcasePages = []string{"course000001"}
	res := e.do(newClient(t), "GET", "/showcase", nil)
	want(t, res, 200, `href="`+own+`"`, own+"_cover.svg")
	if strings.Contains(res.Body, "/a/course000001/") {
		t.Fatal("showcase still links the share-origin address")
	}
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src 'self' http://share.test https://*.page.test") || !strings.Contains(csp, "img-src 'self' data: http://share.test https://*.page.test") {
		t.Fatalf("CSP: %s", csp)
	}
	want(t, e.getHost("share.test", "/security"), 200, "One origin per page", "https://&lt;name&gt;.page.test/", "__Host-", "Public Suffix List")
	want(t, e.getHost("share.test", "/privacy"), 200, "__Host-artifact_unlock")

	// The dashboard (signed in by courseFixture) previews and links the page
	// on its host too.
	want(t, e.get("/app/artifacts"), 200, own)
	want(t, e.get("/app/artifacts/course000001"), 200, `src="`+own+`?byagent_preview=1"`)
}
