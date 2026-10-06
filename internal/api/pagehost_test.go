package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

// pageHostServer runs a server with page hosts on: CONTENT_DOMAIN page.test
// over https, beside the usual share.test and api.test. Requests reach it
// over plain http with the Host header naming the page host, as they do
// behind a TLS-terminating proxy.
func pageHostServer(t *testing.T, scheme string, adjust func(*Deps)) (*httptest.Server, Deps, string) {
	t.Helper()
	return newTestServerWith(t, false, func(d *Deps) {
		d.Cfg.ContentScheme, d.Cfg.ContentDomain = scheme, "page.test"
		d.WebGate = func(w http.ResponseWriter, r *http.Request, g serve.GateView) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<form method="post" action="` + g.Action + `" data-base="` + g.Base + `"></form><link href="` + g.AssetOrigin + `/static/app.css">`))
		}
		if adjust != nil {
			adjust(d)
		}
	})
}

type hostReq struct {
	method, host, path, body string
	hdr                      map[string]string
	cookies                  []*http.Cookie
}

type hostRes struct {
	code int
	body string
	hdr  http.Header
	set  []*http.Cookie
}

// onHost sends one request with the given Host and never follows redirects.
func onHost(t *testing.T, srv *httptest.Server, q hostReq) hostRes {
	t.Helper()
	if q.method == "" {
		q.method = "GET"
	}
	req, err := http.NewRequest(q.method, srv.URL+q.path, strings.NewReader(q.body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = q.host
	if q.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range q.hdr {
		req.Header.Set(k, v)
	}
	for _, c := range q.cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return hostRes{res.StatusCode, string(raw), res.Header, res.Cookies()}
}

func pageOf(t *testing.T, d Deps, id string) store.Artifact {
	t.Helper()
	a, err := d.Store.Artifact(context.Background(), id)
	if err != nil || a.Slug == "" {
		t.Fatalf("artifact %s: %v %+v", id, err, a)
	}
	return a
}

// fromPage is what the page's own fetch sends.
func fromPage(a store.Artifact) map[string]string {
	return map[string]string{"Origin": "https://" + a.Slug + ".page.test"}
}

const threadBody = `{"author_name":"A","body":"hi","anchor":` + anchor + `,"version":1}`

// A page host serves its own page, everything that page needs, and nothing
// else: not another page, not the API, not the dashboard.
func TestPageHostServesOnlyItsPage(t *testing.T) {
	srv, d, tok := pageHostServer(t, "https", nil)
	code, res := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Alpha", []pubFile{{"index.html", "<html><head></head><body><p>hello world alpha</p></body></html>"}, {"s/a.css", "body{}"}}, nil))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, res)
	}
	a := pageOf(t, d, res["id"].(string))
	b := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world beta</p>"))
	host := a.Slug + ".page.test"
	origin := "https://" + host
	if res["url"] != origin+"/" || res["cover_url"] != origin+"/_cover.svg" {
		t.Fatalf("publish output must name the page host: %v", res)
	}

	page := onHost(t, srv, hostReq{host: host, path: "/"})
	if page.code != 200 || !strings.Contains(page.body, "hello world alpha") {
		t.Fatalf("page: %d %s", page.code, page.body)
	}
	for _, want := range []string{
		`<meta property="og:url" content="` + origin + `/">`,
		`<meta property="og:image" content="` + origin + `/_cover.svg">`,
		`data-artifact="` + a.ID + `"`, `data-base="/"`,
		`<link rel="icon" href="http://share.test/favicon.svg">`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("page is missing %s", want)
		}
	}
	csp := page.hdr.Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self';") || strings.Contains(csp, "connect-src 'self' http://share.test") {
		t.Errorf("a page host talks only to itself: %s", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'self' http://api.test") {
		t.Errorf("the dashboard may still frame the page: %s", csp)
	}
	for path, want := range map[string]int{
		"/s/a.css": 200, "/v/1/": 200, "/v/1/s/a.css": 200, "/_cover.svg": 200, "/_rt/comments.js": 200,
		"/c/" + a.ID + "/meta": 200, "/c/" + a.ID + "/comments": 200,
		// Another page, by any route that names it.
		"/a/" + b.ID + "/": 404, "/c/" + b.ID + "/meta": 404, "/c/" + b.ID + "/comments": 404,
		// byagent's own routes are not on a page host.
		"/api/artifacts": 404, "/app/": 404, "/agents": 404, "/healthz": 404, "/skill.md": 404,
		"/_proxy/anything": 403, "/v/9/": 404, "/nope.html": 404,
	} {
		if got := onHost(t, srv, hostReq{host: host, path: path}); got.code != want {
			t.Errorf("GET %s on a's host: %d, want %d (%s)", path, got.code, want, got.body)
		}
	}
	if got := onHost(t, srv, hostReq{method: "POST", host: host, path: "/c/" + b.ID + "/comments", body: threadBody, hdr: fromPage(a)}); got.code != 404 {
		t.Fatalf("a's host posted a comment on b: %d %s", got.code, got.body)
	}
	if n, _ := d.Store.CountOpenThreads(context.Background(), b.ID); n != 0 {
		t.Fatal("b got a thread through a's host")
	}
	// The owner's API takes the host label wherever it takes an id.
	if code, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+a.Slug, tok)); code != 200 || got["id"] != a.ID {
		t.Fatalf("slug as id: %d %v", code, got)
	}

	if got := onHost(t, srv, hostReq{host: "nosuchslug234567.page.test", path: "/"}); got.code != 404 {
		t.Fatalf("unknown page host: %d", got.code)
	}
	if got := onHost(t, srv, hostReq{host: "x." + host, path: "/"}); got.code != 404 {
		t.Fatalf("deeper name: %d", got.code)
	}
	apex := onHost(t, srv, hostReq{host: "page.test", path: "/a/" + a.ID + "/?x=1"})
	if apex.code != 308 || apex.hdr.Get("Location") != "http://share.test/a/"+a.ID+"/?x=1" {
		t.Fatalf("apex: %d %q", apex.code, apex.hdr.Get("Location"))
	}
	// The share and API hosts are untouched.
	if got := onHost(t, srv, hostReq{host: "share.test", path: "/healthz"}); got.code != 200 {
		t.Fatalf("share host: %d", got.code)
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok)); code != 200 {
		t.Fatalf("api: %d", code)
	}
	if err := d.Store.SoftDelete(context.Background(), b.ID, d.Now()); err != nil {
		t.Fatal(err)
	}
	if got := onHost(t, srv, hostReq{host: b.Slug + ".page.test", path: "/"}); got.code != 410 {
		t.Fatalf("deleted page host: %d", got.code)
	}
}

// Old addresses move to the page host with their path and query, so share
// links keep working and the code unlocks on the new host.
func TestLegacyAddressesRedirectToPageHost(t *testing.T) {
	srv, d, tok := pageHostServer(t, "https", nil)
	pub := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world</p>"))
	priv := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>private</p>"))
	coded := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>coded</p>"))
	ctx := context.Background()
	for _, id := range []string{priv.ID, coded.ID} {
		if err := d.Store.SetVisibility(ctx, id, "private"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Store.SetShareCode(ctx, coded.ID, "123456", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		code         int
		location     string
	}{
		{"GET", "/a/" + pub.ID + "/", 308, "https://" + pub.Slug + ".page.test/"},
		{"GET", "/a/" + pub.ID, 308, "https://" + pub.Slug + ".page.test/"},
		{"GET", "/a/" + pub.ID + "/s/a%20b.css?x=1&y=2", 308, "https://" + pub.Slug + ".page.test/s/a%20b.css?x=1&y=2"},
		{"GET", "/a/" + pub.ID + "/v/1/", 308, "https://" + pub.Slug + ".page.test/v/1/"},
		{"GET", "/a/" + pub.ID + "/_cover.svg", 308, "https://" + pub.Slug + ".page.test/_cover.svg"},
		{"GET", "/a/" + coded.ID + "/?k=123456", 308, "https://" + coded.Slug + ".page.test/?k=123456"},
		{"POST", "/a/" + coded.ID + "/_unlock", 308, "https://" + coded.Slug + ".page.test/_unlock"},
		// Private with no way in for visitors: the redirect would name its
		// host to someone holding only the id.
		{"GET", "/a/" + priv.ID + "/", 404, ""},
		{"GET", "/a/nosuchpage00/", 404, ""},
	} {
		got := onHost(t, srv, hostReq{method: tc.method, host: "share.test", path: tc.path})
		if got.code != tc.code || got.hdr.Get("Location") != tc.location {
			t.Errorf("%s %s: %d %q, want %d %q", tc.method, tc.path, got.code, got.hdr.Get("Location"), tc.code, tc.location)
		}
		if tc.code == 308 && strings.Contains(tc.path, "?k=") && got.hdr.Get("Cache-Control") != "no-store" {
			t.Errorf("a redirect carrying a code must not be cached: %q", got.hdr.Get("Cache-Control"))
		}
	}
	// The share link lands on the page host and unlocks there.
	link := onHost(t, srv, hostReq{host: coded.Slug + ".page.test", path: "/?k=123456"})
	if link.code != 303 || link.hdr.Get("Location") != "/" || len(link.set) != 1 {
		t.Fatalf("unlock on the page host: %d %q %v", link.code, link.hdr.Get("Location"), link.set)
	}
	if got := onHost(t, srv, hostReq{host: coded.Slug + ".page.test", path: "/", cookies: link.set}); got.code != 200 || !strings.Contains(got.body, "coded") {
		t.Fatalf("unlocked page: %d %s", got.code, got.body)
	}
	if err := d.Store.SoftDelete(ctx, pub.ID, d.Now()); err != nil {
		t.Fatal(err)
	}
	if got := onHost(t, srv, hostReq{host: "share.test", path: "/a/" + pub.ID + "/"}); got.code != 410 {
		t.Fatalf("deleted: %d", got.code)
	}
	// The API and the dashboard links name the page host.
	code, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+coded.ID, tok))
	if code != 200 || got["share_url"] != "https://"+coded.Slug+".page.test/?k=123456" {
		t.Fatalf("share_url: %d %v", code, got["share_url"])
	}
}

// Cookies on a page host are host-only and, over https, __Host- prefixed.
// The cookie one page minted opens no other page.
func TestPageHostCookies(t *testing.T) {
	for _, scheme := range []string{"https", "http"} {
		t.Run(scheme, func(t *testing.T) {
			srv, d, tok := pageHostServer(t, scheme, nil)
			a := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world a</p>"))
			b := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world b</p>"))
			ctx := context.Background()
			for _, p := range []store.Artifact{a, b} {
				if err := d.Store.SetVisibility(ctx, p.ID, "private"); err != nil {
					t.Fatal(err)
				}
				if err := d.Store.SetShareCode(ctx, p.ID, "123456", nil); err != nil {
					t.Fatal(err)
				}
			}
			res := onHost(t, srv, hostReq{host: a.Slug + ".page.test", path: "/?k=123456"})
			if len(res.set) != 1 {
				t.Fatalf("cookies: %v", res.set)
			}
			c := res.set[0]
			raw := res.hdr.Get("Set-Cookie")
			wantName := "__Host-artifact_unlock"
			if scheme == "http" {
				wantName = "artifact_unlock"
			}
			if c.Name != wantName || c.Path != "/" || c.Domain != "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != (scheme == "https") {
				t.Fatalf("cookie: %+v (%s)", c, raw)
			}
			if strings.Contains(strings.ToLower(raw), "domain=") {
				t.Fatalf("a page cookie must be host-only: %s", raw)
			}
			// Even if a browser did send a's cookie to b's host, it opens nothing:
			// it is signed over a's id.
			if got := onHost(t, srv, hostReq{host: b.Slug + ".page.test", path: "/", cookies: []*http.Cookie{c}}); got.code == 200 && strings.Contains(got.body, "hello world b") {
				t.Fatal("a's unlock opened b")
			}
			if got := onHost(t, srv, hostReq{host: b.Slug + ".page.test", path: "/c/" + b.ID + "/meta", cookies: []*http.Cookie{c}}); got.code != 404 {
				t.Fatalf("a's unlock read b's comments: %d", got.code)
			}
			// The page's own comment routes admit the unlocked reader.
			if got := onHost(t, srv, hostReq{host: a.Slug + ".page.test", path: "/c/" + a.ID + "/meta", cookies: []*http.Cookie{c}}); got.code != 200 {
				t.Fatalf("unlocked reader's comments: %d %s", got.code, got.body)
			}
			// The gate on a page host posts to its own host and loads its
			// assets from the share origin.
			gate := onHost(t, srv, hostReq{host: a.Slug + ".page.test", path: "/"})
			if !strings.Contains(gate.body, `action="/_unlock"`) || !strings.Contains(gate.body, `href="http://share.test/static/app.css"`) {
				t.Fatalf("gate: %d %s", gate.code, gate.body)
			}
			if !strings.Contains(gate.hdr.Get("Content-Security-Policy"), "style-src 'self' http://share.test") {
				t.Fatalf("gate CSP: %s", gate.hdr.Get("Content-Security-Policy"))
			}
		})
	}
}

// Page access control (#74) on a page host: the gate's routes sit at the
// host's root, the grant is a host-only cookie, the mailed link names the
// page host, and the gate's forms obey the same-origin rule.
func TestPageHostAccessControl(t *testing.T) {
	mail := &captureMail{}
	srv, d, tok := pageHostServer(t, "https", func(d *Deps) { d.Mail = mail })
	base := srv.URL
	form := func(a store.Artifact, path string, v url.Values, origin string) hostRes {
		h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
		if origin != "" {
			h["Origin"] = origin
		}
		return onHost(t, srv, hostReq{method: "POST", host: a.Slug + ".page.test", path: path, body: v.Encode(), hdr: h})
	}
	grant := func(res hostRes) *http.Cookie {
		t.Helper()
		for _, c := range res.set {
			if c.Name == "__Host-artifact_access" && c.Value != "" {
				if c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly {
					t.Fatalf("grant cookie: %+v", c)
				}
				return c
			}
		}
		t.Fatalf("no grant: %d %v %s", res.code, res.set, res.body)
		return nil
	}

	// Password.
	pw := pageOf(t, d, publishOne(t, base, tok, "<p>hello world pw</p>"))
	other := pageOf(t, d, publishOne(t, base, tok, "<p>hello world other</p>"))
	if code, a := patchAccess(t, base, tok, pw.ID, `{"mode":"password","password":"hunter2hunter2"}`); code != 200 || a["share_url"] != "https://"+pw.Slug+".page.test/" {
		t.Fatalf("password: %d %v", code, a)
	}
	// The legacy address still finds a password page: it has a door.
	if res := onHost(t, srv, hostReq{host: "share.test", path: "/a/" + pw.ID + "/"}); res.code != 308 || res.hdr.Get("Location") != "https://"+pw.Slug+".page.test/" {
		t.Fatalf("legacy password page: %d %s", res.code, res.hdr.Get("Location"))
	}
	gate := onHost(t, srv, hostReq{host: pw.Slug + ".page.test", path: "/"})
	if !strings.Contains(gate.body, `action="/_unlock"`) || !strings.Contains(gate.body, `data-base="/"`) {
		t.Fatalf("gate: %d %s", gate.code, gate.body)
	}
	pwForm := url.Values{"password": {"hunter2hunter2"}}
	for _, o := range []string{"https://" + other.Slug + ".page.test", "https://evil.example", ""} {
		if res := form(pw, "/_unlock", pwForm, o); res.code != 403 {
			t.Fatalf("password from %q: %d", o, res.code)
		}
	}
	res := form(pw, "/_unlock", pwForm, "https://"+pw.Slug+".page.test")
	if res.code != 303 || res.hdr.Get("Location") != "/" {
		t.Fatalf("password: %d %s", res.code, res.hdr.Get("Location"))
	}
	c := grant(res)
	if got := onHost(t, srv, hostReq{host: pw.Slug + ".page.test", path: "/", cookies: []*http.Cookie{c}}); got.code != 200 || !strings.Contains(got.body, "hello world pw") {
		t.Fatalf("with the password: %d %s", got.code, got.body)
	}
	if got := onHost(t, srv, hostReq{host: other.Slug + ".page.test", path: "/c/" + pw.ID + "/meta", cookies: []*http.Cookie{c}}); got.code != 404 {
		t.Fatalf("another host answered for the page: %d", got.code)
	}

	// Restricted, by mail.
	rs := pageOf(t, d, publishOne(t, base, tok, "<p>hello world restricted</p>"))
	if code, a := patchAccess(t, base, tok, rs.ID, `{"mode":"restricted","allowed_domains":["corp.example"]}`); code != 200 {
		t.Fatalf("restricted: %d %v", code, a)
	}
	own := "https://" + rs.Slug + ".page.test"
	ask := url.Values{"email": {"lee@corp.example"}}
	for _, o := range []string{"https://" + other.Slug + ".page.test", "https://evil.example"} {
		if res := form(rs, "/_access/email", ask, o); res.code != 403 {
			t.Fatalf("ask from %s: %d", o, res.code)
		}
	}
	if mail.count() != 0 {
		t.Fatal("a cross-page post sent mail")
	}
	if res := form(rs, "/_access/email", ask, own); res.code != 200 || !strings.Contains(res.body, `data-base="/"`) {
		t.Fatalf("ask: %d %s", res.code, res.body)
	}
	_, text := mail.waitMail(t, 1)
	m := mailCodeRe.FindStringSubmatch(text)
	link := regexp.MustCompile(`https://\S+`).FindString(text)
	if m == nil || !strings.HasPrefix(link, own+"/_access/link?t=") {
		t.Fatalf("mail: %q", text)
	}
	u, _ := url.Parse(link)
	if got := onHost(t, srv, hostReq{host: rs.Slug + ".page.test", path: u.RequestURI()}); got.code != 200 {
		t.Fatalf("open the link: %d %s", got.code, got.body)
	}
	if res := form(rs, "/_access/link", url.Values{"t": {u.Query().Get("t")}}, "https://"+other.Slug+".page.test"); res.code != 403 {
		t.Fatalf("link spent cross-page: %d", res.code)
	}
	res = form(rs, "/_access/link", url.Values{"t": {u.Query().Get("t")}}, own)
	if res.code != 303 || res.hdr.Get("Location") != "/" {
		t.Fatalf("link: %d %s", res.code, res.body)
	}
	c = grant(res)
	if got := onHost(t, srv, hostReq{host: rs.Slug + ".page.test", path: "/c/" + rs.ID + "/meta", cookies: []*http.Cookie{c}}); got.code != 200 {
		t.Fatalf("restricted reader's comments: %d", got.code)
	}
	// The mailed code works too (a new one; the link spent the first).
	if res := form(rs, "/_access/email", ask, own); res.code != 200 {
		t.Fatalf("ask again: %d", res.code)
	}
	_, text = mail.waitMail(t, 2)
	m = mailCodeRe.FindStringSubmatch(text)
	res = form(rs, "/_access/code", url.Values{"email": {"lee@corp.example"}, "code": {m[1]}}, own)
	if res.code != 303 || res.hdr.Get("Location") != "/" {
		t.Fatalf("code: %d %s", res.code, res.body)
	}
	grant(res)
}

// Page hosts are one site until the content domain is on the Public Suffix
// List, so every write must carry the page's own origin.
func TestPageHostRefusesCrossPageWrites(t *testing.T) {
	srv, d, tok := pageHostServer(t, "https", nil)
	a := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world</p>"))
	b := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world</p>"))
	host := a.Slug + ".page.test"
	own := "https://" + host
	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+a.ID+"/comments", threadBody, nil))
	if code != 201 {
		t.Fatalf("seed thread on the share origin: %d %v", code, th)
	}
	tid := th["id"].(string)
	writes := []struct{ path, body string }{
		{"/c/" + a.ID + "/comments", threadBody},
		{"/c/" + a.ID + "/comments/" + tid + "/reply", `{"author_name":"A","body":"reply"}`},
		{"/c/" + a.ID + "/comments/" + tid + "/seen", `{"version":1,"anchor_status":"attached"}`},
		{"/_unlock", "code=123456"},
	}
	refused := []map[string]string{
		{"Origin": "https://" + b.Slug + ".page.test"},
		{"Origin": "https://" + b.Slug + ".page.test", "Sec-Fetch-Site": "same-site"},
		{"Origin": "http://share.test"},
		{"Origin": "https://evil.example"},
		{"Origin": "null", "Sec-Fetch-Site": "cross-site"},
		{"Origin": "null", "Sec-Fetch-Site": "same-site"},
		{"Sec-Fetch-Site": "same-site"},
		{}, // neither header: not a browser on this page
	}
	for _, wr := range writes {
		for _, hdr := range refused {
			got := onHost(t, srv, hostReq{method: "POST", host: host, path: wr.path, body: wr.body, hdr: hdr})
			if got.code != 403 {
				t.Errorf("POST %s with %v: %d, want 403 (%s)", wr.path, hdr, got.code, got.body)
			}
		}
	}
	threads, _ := d.Store.Threads(context.Background(), a.ID, "", time.Time{}, 0)
	if len(threads) != 1 {
		t.Fatalf("a refused write still made a thread: %d", len(threads))
	}
	for _, hdr := range []map[string]string{
		{"Origin": own},
		{"Origin": strings.ToUpper(own)},
		{"Origin": "null", "Sec-Fetch-Site": "same-origin"}, // a form on the no-referrer gate
		{"Sec-Fetch-Site": "same-origin"},
	} {
		if got := onHost(t, srv, hostReq{method: "POST", host: host, path: writes[0].path, body: writes[0].body, hdr: hdr}); got.code != 201 {
			t.Errorf("own page with %v: %d %s", hdr, got.code, got.body)
		}
		if got := onHost(t, srv, hostReq{method: "POST", host: host, path: writes[1].path, body: writes[1].body, hdr: hdr}); got.code != 201 {
			t.Errorf("own reply with %v: %d %s", hdr, got.code, got.body)
		}
	}
	// Reads are never refused: the browser keeps their answers from other
	// origins anyway.
	if got := onHost(t, srv, hostReq{host: host, path: "/c/" + a.ID + "/comments", hdr: map[string]string{"Origin": "https://" + b.Slug + ".page.test"}}); got.code != 200 {
		t.Fatalf("read: %d", got.code)
	}
	if got := onHost(t, srv, hostReq{method: "DELETE", host: host, path: "/", hdr: fromPage(a)}); got.code != 405 {
		t.Fatalf("delete from the page itself: %d", got.code)
	}
}

// Views on a page host are counted for the page, as on the share origin.
func TestPageHostCountsViews(t *testing.T) {
	var seen []string
	srv, d, tok := pageHostServer(t, "https", func(d *Deps) {
		d.OnView = func(r *http.Request, id, path string) { seen = append(seen, id+" "+path) }
	})
	a := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world</p>"))
	onHost(t, srv, hostReq{host: a.Slug + ".page.test", path: "/"})
	onHost(t, srv, hostReq{host: a.Slug + ".page.test", path: "/v/1/"})
	if len(seen) != 1 || seen[0] != a.ID+" index.html" {
		t.Fatalf("views: %v", seen)
	}
}

// Without CONTENT_DOMAIN nothing changes: pages live under /a/{id}/ and a
// would-be page host is just an unknown Host.
func TestNoContentDomainKeepsSharedOrigin(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	a := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>hello world</p>"))
	if got := onHost(t, srv, hostReq{host: "share.test", path: "/a/" + a.ID + "/"}); got.code != 200 || !strings.Contains(got.body, `<meta property="og:url" content="http://share.test/a/`+a.ID+`/">`) {
		t.Fatalf("share origin page: %d %s", got.code, got.body)
	}
	if got := onHost(t, srv, hostReq{host: a.Slug + ".page.test", path: "/"}); strings.Contains(got.body, "hello world") {
		t.Fatal("a page host answered without a content domain")
	}
	if got := onHost(t, srv, hostReq{method: "POST", host: "share.test", path: "/c/" + a.ID + "/comments", body: threadBody}); got.code != 201 {
		t.Fatalf("share origin comments keep working: %d %s", got.code, got.body)
	}
}
