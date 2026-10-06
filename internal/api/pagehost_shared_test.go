package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/serve"
)

// sharedHostServer runs page hosts under the share host itself, the way
// production does with CONTENT_DOMAIN=byagent.dev: pages at
// {slug}.share.test beside share.test and api.test.
func sharedHostServer(t *testing.T) (srvURL string, onH func(hostReq) hostRes, d Deps, tok string) {
	t.Helper()
	srv, d, tok := newTestServerWith(t, false, func(d *Deps) {
		d.Cfg.ContentScheme, d.Cfg.ContentDomain, d.Cfg.ContentShared = "https", "share.test", true
		d.WebGate = func(w http.ResponseWriter, r *http.Request, g serve.GateView) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<form method="post" action="` + g.Action + `"></form>`))
		}
	})
	return srv.URL, func(q hostReq) hostRes { return onHost(t, srv, q) }, d, tok
}

func TestSharedContentDomain(t *testing.T) {
	base, on, d, tok := sharedHostServer(t)
	a := pageOf(t, d, publishOne(t, base, tok, "<p>hello world shared</p>"))
	host := a.Slug + ".share.test"
	origin := "https://" + host

	code, got := do(t, authReq(t, "GET", base+"/api/artifacts/"+a.ID, tok))
	if code != 200 || got["url"] != origin+"/" {
		t.Fatalf("url: %d %v", code, got["url"])
	}

	// The slug host serves the page and nothing of byagent's own.
	if res := on(hostReq{host: host, path: "/"}); res.code != 200 || !strings.Contains(res.body, "hello world shared") {
		t.Fatalf("page host: %d %s", res.code, res.body)
	}
	for _, p := range []string{"/healthz", "/api/artifacts", "/app/", "/agents"} {
		if res := on(hostReq{host: host, path: p}); res.code != 404 {
			t.Errorf("%s on the page host: %d", p, res.code)
		}
	}

	// Every other name keeps today's routes: no apex redirect (it would
	// loop), no 404 for labels that are not slugs.
	for _, h := range []string{"share.test", "www.share.test", "api.test", "app.share.test", "domains.share.test",
		"x." + host, a.Slug[:15] + ".share.test", a.Slug + "x.share.test"} {
		if res := on(hostReq{host: h, path: "/healthz"}); res.code != 200 {
			t.Errorf("%s/healthz: %d %s", h, res.code, res.hdr.Get("Location"))
		}
	}
	if res := on(hostReq{host: "share.test", path: "/a/" + a.ID + "/"}); res.code != 308 || res.hdr.Get("Location") != origin+"/" {
		t.Fatalf("legacy address: %d %s", res.code, res.hdr.Get("Location"))
	}

	// A page host cannot write to byagent's own hosts: SameSite=Lax does
	// not stop it, since it is the same site.
	authJSON := map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"}
	for _, h := range []string{"share.test", "api.test", "app.share.test"} {
		for _, p := range []string{"/api/artifacts/" + a.ID + "/share-code", "/c/" + a.ID + "/comments", "/app/logout", "/app/login/email"} {
			hdr := map[string]string{"Origin": origin}
			for k, v := range authJSON {
				hdr[k] = v
			}
			if res := on(hostReq{method: "POST", host: h, path: p, body: "{}", hdr: hdr}); res.code != 403 {
				t.Errorf("POST %s%s from a page host: %d", h, p, res.code)
			}
		}
	}
	// A read is not refused, and byagent's own origins still write.
	if res := on(hostReq{host: "share.test", path: "/healthz", hdr: map[string]string{"Origin": origin}}); res.code != 200 {
		t.Fatalf("GET from a page host: %d", res.code)
	}
	if res := on(hostReq{method: "POST", host: "api.test", path: "/api/artifacts/" + a.ID + "/share-code", body: "{}",
		hdr: map[string]string{"Authorization": "Bearer " + tok, "Origin": "http://api.test"}}); res.code == 403 {
		t.Fatalf("the API refused its own origin: %d %s", res.code, res.body)
	}

	// Cookies on the page host are host-only __Host- cookies.
	ctx := context.Background()
	if err := d.Store.SetVisibility(ctx, a.ID, "private"); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.SetShareCode(ctx, a.ID, "123456", nil); err != nil {
		t.Fatal(err)
	}
	res := on(hostReq{host: host, path: "/?k=123456"})
	if len(res.set) != 1 {
		t.Fatalf("cookies: %v", res.set)
	}
	c := res.set[0]
	if c.Name != "__Host-artifact_unlock" || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly {
		t.Fatalf("cookie: %+v", c)
	}
	if strings.Contains(strings.ToLower(res.hdr.Get("Set-Cookie")), "domain=") {
		t.Fatalf("a page cookie must be host-only: %s", res.hdr.Get("Set-Cookie"))
	}
	// Sibling page hosts still need their own Origin to write.
	b := pageOf(t, d, publishOne(t, base, tok, "<p>b</p>"))
	if res := on(hostReq{method: "POST", host: host, path: "/c/" + a.ID + "/comments", body: threadBody, cookies: []*http.Cookie{c},
		hdr: map[string]string{"Origin": "https://" + b.Slug + ".share.test"}}); res.code != 403 {
		t.Fatalf("sibling page posted: %d", res.code)
	}
	if res := on(hostReq{method: "POST", host: host, path: "/c/" + a.ID + "/comments", body: threadBody, cookies: []*http.Cookie{c},
		hdr: map[string]string{"Origin": origin}}); res.code != 201 {
		t.Fatalf("own page posted: %d %s", res.code, res.body)
	}
}
