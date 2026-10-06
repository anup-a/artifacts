package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anup-a/artifacts/internal/domains"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/serve"
)

type txtMap struct {
	mu sync.Mutex
	m  map[string][]string
}

func (t *txtMap) LookupTXT(_ context.Context, name string) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.m[name]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (t *txtMap) set(name, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[name] = []string{value}
}

// domainServer is a test server with custom domains on: the none provider,
// TXT records from a map, and a gate that echoes where its form posts.
func domainServer(t *testing.T) (*httptest.Server, Deps, string, *txtMap) {
	t.Helper()
	dns := &txtMap{m: map[string][]string{}}
	srv, d, tok := newTestServerWith(t, false, func(d *Deps) {
		d.Domains = &domains.Service{Store: d.Store, Provider: domains.None{}, Resolver: dns, Now: d.Now,
			Target: "domains.share.example", Scheme: "http", Own: []string{"share.test", "api.test", "domains.share.example"},
			MaxDomains: 3, Index: &domains.Index{Store: d.Store}}
		d.WebGate = func(w http.ResponseWriter, r *http.Request, g serve.GateView) {
			w.WriteHeader(200)
			w.Write([]byte("gate action=" + g.Action + " assets=" + g.AssetOrigin))
		}
	})
	return srv, d, tok, dns
}

func domainReq(t *testing.T, method, url, host, body string) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && method != "HEAD" {
		// What a browser sends from a page on the domain itself.
		req.Header.Set("Origin", "http://"+host)
	}
	return req
}

func fetch(t *testing.T, req *http.Request) (*http.Response, string) {
	t.Helper()
	res, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func authJSON(t *testing.T, method, url, tok, body string) *http.Request {
	t.Helper()
	req := jsonReq(t, method, url, body, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	return req
}

// connect adds a domain over the API, publishes its TXT record and polls
// it active.
func connectDomain(t *testing.T, srvURL, tok string, dns *txtMap, host string) {
	t.Helper()
	code, b := do(t, authJSON(t, "POST", srvURL+"/api/domains", tok, `{"domain":"`+host+`"}`))
	if code != 201 && code != 200 {
		t.Fatalf("add %s: %d %v", host, code, b)
	}
	for _, r := range b["dns_instructions"].([]any) {
		rec := r.(map[string]any)
		if rec["type"] == "TXT" {
			dns.set(rec["name"].(string), rec["value"].(string))
		}
	}
	code, b = do(t, authReq(t, "GET", srvURL+"/api/domains/"+host, tok))
	if code != 200 || b["status"] != "active" {
		t.Fatalf("activate %s: %d %v", host, code, b)
	}
}

func mount(t *testing.T, srvURL, tok, host, path, id string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path, "artifact_id": id})
	return do(t, authJSON(t, "POST", srvURL+"/api/domains/"+host+"/mounts", tok, string(body)))
}

func TestDomainsOffAnswers404(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	code, b := do(t, authReq(t, "GET", srv.URL+"/api/domains", tok))
	if code != 404 || !strings.Contains(b["error"].(map[string]any)["message"].(string), "not turned on") {
		t.Fatalf("%d %v", code, b)
	}
	// And no Host is treated as a custom domain.
	id := publishOne(t, srv.URL, tok, "<p>hi</p>")
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/a/"+id+"/", "docs.example.com", "")); res.StatusCode != 200 {
		t.Fatalf("serving changed with domains off: %d", res.StatusCode)
	}
}

func TestDomainAPI(t *testing.T) {
	srv, d, tok, dns := domainServer(t)
	other := mintKey(t, d, "personal")

	code, b := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"https://Docs.Example.com/"}`))
	if code != 201 || b["domain"] != "docs.example.com" || b["status"] != "pending" || b["verified"] != false {
		t.Fatalf("add: %d %v", code, b)
	}
	recs := b["dns_instructions"].([]any)
	cname, txt := recs[0].(map[string]any), recs[1].(map[string]any)
	if cname["type"] != "CNAME" || cname["host"] != "docs" || cname["value"] != "domains.share.example" {
		t.Fatalf("cname: %v", cname)
	}
	if txt["type"] != "TXT" || txt["name"] != "_byagent.docs.example.com" || !strings.HasPrefix(txt["value"].(string), "byagent-verify=") {
		t.Fatalf("txt: %v", txt)
	}
	if b["verification"].(map[string]any)["state"] != "verifying" {
		t.Fatalf("verification: %v", b["verification"])
	}
	if code, again := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"docs.example.com"}`)); code != 200 || again["dns_instructions"].([]any)[1].(map[string]any)["value"] != txt["value"] {
		t.Fatalf("re-add: %d %v", code, again)
	}
	for _, bad := range []string{"share.test", "evil.share.test", "localhost", "10.0.0.1", "docs.example.com:443", "*.example.com", ""} {
		if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"`+bad+`"}`)); code != 400 {
			t.Errorf("%q: %d %v", bad, code, b)
		}
	}
	// Another workspace can neither take it nor see it.
	if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/domains", other, `{"domain":"docs.example.com"}`)); code != 409 || b["error"].(map[string]any)["code"] != "CONFLICT" {
		t.Fatalf("taken: %d %v", code, b)
	}
	for _, req := range []*http.Request{
		authReq(t, "GET", srv.URL+"/api/domains/docs.example.com", other),
		authReq(t, "DELETE", srv.URL+"/api/domains/docs.example.com", other),
		authReq(t, "GET", srv.URL+"/api/domains/docs.example.com/mounts", other),
	} {
		if code, b := do(t, req); code != 404 {
			t.Fatalf("other workspace %s %s: %d %v", req.Method, req.URL.Path, code, b)
		}
	}
	if code, b := do(t, authReq(t, "GET", srv.URL+"/api/domains", other)); code != 200 || len(b["domains"].([]any)) != 0 {
		t.Fatalf("other list: %d %v", code, b)
	}

	// A wrong TXT value keeps it pending; the right one activates it.
	dns.set("_byagent.docs.example.com", "byagent-verify=guess")
	if code, b := do(t, authReq(t, "GET", srv.URL+"/api/domains/docs.example.com", tok)); code != 200 || b["verified"] != false ||
		!strings.Contains(b["verification"].(map[string]any)["message"].(string), "does not hold") {
		t.Fatalf("wrong txt: %d %v", code, b)
	}
	dns.set("_byagent.docs.example.com", txt["value"].(string))
	// The check is throttled per domain, so wait it out by forcing one.
	dom, _ := d.Domains.Owned(context.Background(), tenantOf(t, d, tok), "docs.example.com")
	d.Domains.Refresh(context.Background(), dom, true)
	if code, b := do(t, authReq(t, "GET", srv.URL+"/api/domains/docs.example.com", tok)); code != 200 || b["status"] != "active" || b["verification"] != nil {
		t.Fatalf("active: %d %v", code, b)
	}

	mine := publishOne(t, srv.URL, tok, "<p>mine</p>")
	theirs := publishOne(t, srv.URL, other, "<p>theirs</p>")
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "/", theirs); code != 404 {
		t.Fatalf("mounting another workspace's page: %d %v", code, b)
	}
	if code, b := mount(t, srv.URL, other, "docs.example.com", "/", theirs); code != 404 {
		t.Fatalf("mounting on another workspace's domain: %d %v", code, b)
	}
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "/_rt", mine); code != 400 {
		t.Fatalf("reserved path: %d %v", code, b)
	}
	code, b = mount(t, srv.URL, tok, "docs.example.com", "", mine)
	if code != 200 || b["url"] != "http://docs.example.com/" || b["path"] != "/" || b["primary"] != false {
		t.Fatalf("mount: %d %v", code, b)
	}
	code, b = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+mine, tok))
	if code != 200 || len(b["domains"].([]any)) != 1 || b["primary_url"] != nil {
		t.Fatalf("artifact domains: %d %v", code, b)
	}

	// Primary domain.
	code, b = do(t, authJSON(t, "POST", srv.URL+"/api/artifacts/"+mine+"/primary-domain", tok, `{"domain":"docs.example.com"}`))
	if code != 200 || b["primary_url"] != "http://docs.example.com/" || b["primary"].(map[string]any)["domain"] != "docs.example.com" {
		t.Fatalf("primary: %d %v", code, b)
	}
	if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/artifacts/"+theirs+"/primary-domain", tok, `{"domain":"docs.example.com"}`)); code != 404 {
		t.Fatalf("primary on someone else's page: %d %v", code, b)
	}
	code, b = do(t, authJSON(t, "POST", srv.URL+"/api/artifacts/"+mine+"/primary-domain", tok, `{"domain":null}`))
	if code != 200 || b["primary"] != nil || b["primary_url"] != nil {
		t.Fatalf("clear primary: %d %v", code, b)
	}

	if code, b := do(t, authReq(t, "DELETE", srv.URL+"/api/domains/docs.example.com/mounts?path=/", tok)); code != 204 {
		t.Fatalf("unmount: %d %v", code, b)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/domains/docs.example.com", tok)); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/domains/docs.example.com", tok)); code != 404 {
		t.Fatalf("after delete: %d", code)
	}
}

func TestDomainQuotaAndGuests(t *testing.T) {
	srv, d, tok, _ := domainServer(t)
	for _, h := range []string{"a.example.com", "b.example.com", "c.example.com"} {
		if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"`+h+`"}`)); code != 201 {
			t.Fatalf("%s: %d %v", h, code, b)
		}
	}
	if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"d.example.com"}`)); code != 429 || b["error"].(map[string]any)["code"] != "QUOTA_EXCEEDED" {
		t.Fatalf("quota: %d %v", code, b)
	}
	plain, hash := ids.NewKey()
	if _, err := d.Store.CreateGuest(context.Background(), hash, plain[:9], d.Now()); err != nil {
		t.Fatal(err)
	}
	if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/domains", plain, `{"domain":"g.example.com"}`)); code != 403 {
		t.Fatalf("guest: %d %v", code, b)
	}
}

func TestCustomHostServesMountsOnly(t *testing.T) {
	srv, _, tok, dns := domainServer(t)
	root := publishOne(t, srv.URL, tok, "<html><head></head><body><p>root page</p></body></html>")
	guide := publishOne(t, srv.URL, tok, "<html><body><p>guide page</p></body></html>")
	elsewhere := publishOne(t, srv.URL, tok, "<p>not mounted</p>")

	// Added but unverified: known, and serves nothing at all.
	if code, _ := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"docs.example.com"}`)); code != 201 {
		t.Fatal(code)
	}
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "", root); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	res, body := fetch(t, domainReq(t, "GET", srv.URL+"/", "docs.example.com", ""))
	if res.StatusCode != 404 || strings.Contains(body, "root page") {
		t.Fatalf("unverified domain served: %d %s", res.StatusCode, body)
	}
	if res, body := fetch(t, domainReq(t, "GET", srv.URL+"/a/"+root+"/", "docs.example.com", "")); res.StatusCode != 404 || strings.Contains(body, "root page") {
		t.Fatalf("unverified domain reached the share routes: %d", res.StatusCode)
	}

	connectDomain(t, srv.URL, tok, dns, "docs.example.com")
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "/guide", guide); code != 200 {
		t.Fatalf("%d %v", code, b)
	}

	res, body = fetch(t, domainReq(t, "GET", srv.URL+"/", "Docs.Example.com:443", ""))
	if res.StatusCode != 200 || !strings.Contains(body, "root page") {
		t.Fatalf("root: %d %s", res.StatusCode, body)
	}
	if !strings.Contains(body, `data-base="/"`) || !strings.Contains(body, `data-artifact="`+root+`"`) {
		t.Fatalf("runtime tag: %s", body)
	}
	if !strings.Contains(body, `og:image" content="http://docs.example.com/_cover.svg"`) || strings.Contains(body, "/favicon.svg") {
		t.Fatalf("meta: %s", body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors") || !strings.Contains(csp, "style-src 'self' http://share.test") {
		t.Fatalf("csp: %q", csp)
	}

	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/guide", "docs.example.com", "")); res.StatusCode != 301 || res.Header.Get("Location") != "/guide/" {
		t.Fatalf("guide redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, body := fetch(t, domainReq(t, "GET", srv.URL+"/guide/", "docs.example.com", "")); res.StatusCode != 200 || !strings.Contains(body, "guide page") || !strings.Contains(body, `data-base="/guide/"`) {
		t.Fatalf("guide: %d %s", res.StatusCode, body)
	}
	if res, body := fetch(t, domainReq(t, "GET", srv.URL+"/guide/v/1/", "docs.example.com", "")); res.StatusCode != 200 || !strings.Contains(body, "guide page") {
		t.Fatalf("pinned: %d %s", res.StatusCode, body)
	}
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/_cover.svg", "docs.example.com", "")); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("cover: %d", res.StatusCode)
	}
	if res, body := fetch(t, domainReq(t, "GET", srv.URL+"/_rt/comments.js", "docs.example.com", "")); res.StatusCode != 200 || body != "/*runtime*/" {
		t.Fatalf("runtime: %d %s", res.StatusCode, body)
	}

	// Nothing but the mounts: no other page, no API, no dashboard.
	for _, p := range []string{"/a/" + elsewhere + "/", "/guide/a/" + elsewhere + "/", "/api/artifacts", "/app/settings", "/healthz", "/missing.html"} {
		req := domainReq(t, "GET", srv.URL+p, "docs.example.com", "")
		req.Header.Set("Authorization", "Bearer "+tok)
		res, body := fetch(t, req)
		if res.StatusCode != 404 || strings.Contains(body, "not mounted") || strings.Contains(body, "artifacts\"") {
			t.Fatalf("%s on a custom domain: %d %s", p, res.StatusCode, body)
		}
	}
	post := domainReq(t, "POST", srv.URL+"/api/artifacts", "docs.example.com", `{}`)
	post.Header.Set("Origin", "http://docs.example.com")
	if res, _ := fetch(t, post); res.StatusCode != 405 {
		t.Fatalf("POST on a custom domain: %d", res.StatusCode)
	}
	// Writes must come from the domain itself.
	for _, o := range []string{"", "https://evil.example", "http://other.example.com", "http://share.test"} {
		req := domainReq(t, "POST", srv.URL+"/c/"+root+"/comments", "docs.example.com", `{}`)
		req.Header.Del("Origin")
		if o != "" {
			req.Header.Set("Origin", o)
		}
		if res, _ := fetch(t, req); res.StatusCode != 403 {
			t.Fatalf("POST from %q on a custom domain: %d", o, res.StatusCode)
		}
	}

	// Comments: only for pages mounted here.
	thread := `{"author_name":"A","body":"on my domain","anchor":` + anchor + `,"version":1}`
	if res, body := fetch(t, domainReq(t, "POST", srv.URL+"/c/"+root+"/comments", "docs.example.com", thread)); res.StatusCode != 201 {
		t.Fatalf("comment: %d %s", res.StatusCode, body)
	}
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/c/"+guide+"/meta", "docs.example.com", "")); res.StatusCode != 200 {
		t.Fatalf("meta: %d", res.StatusCode)
	}
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/c/"+elsewhere+"/meta", "docs.example.com", "")); res.StatusCode != 404 {
		t.Fatalf("an unmounted page's comments on a custom domain: %d", res.StatusCode)
	}

	// Hosts nobody connected carry on as before.
	if res, body := fetch(t, domainReq(t, "GET", srv.URL+"/a/"+elsewhere+"/", "unconnected.example.org", "")); res.StatusCode != 200 || !strings.Contains(body, "not mounted") {
		t.Fatalf("unknown host: %d", res.StatusCode)
	}
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/healthz", "127.0.0.1:8080", "")); res.StatusCode != 200 {
		t.Fatalf("health check: %d", res.StatusCode)
	}

	// A deleted page stops being served on its domain.
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+guide, tok)); code != 204 {
		t.Fatal(code)
	}
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/guide/", "docs.example.com", "")); res.StatusCode == 200 {
		t.Fatal("deleted page still served")
	}
}

func TestCustomHostPrivatePages(t *testing.T) {
	srv, d, tok, dns := domainServer(t)
	ctx := context.Background()
	id := publishOne(t, srv.URL, tok, "<html><body><p>secret</p></body></html>")
	connectDomain(t, srv.URL, tok, dns, "docs.example.com")
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "/team", id); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	if err := d.Store.SetVisibility(ctx, id, "private"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/team/", "/team/index.html", "/team/v/1/", "/team/_cover.svg"} {
		if res, body := fetch(t, domainReq(t, "GET", srv.URL+p, "docs.example.com", "")); res.StatusCode != 404 || strings.Contains(body, "secret") {
			t.Fatalf("private %s on a custom domain: %d %s", p, res.StatusCode, body)
		}
	}
	if res, _ := fetch(t, domainReq(t, "GET", srv.URL+"/c/"+id+"/meta", "docs.example.com", "")); res.StatusCode != 404 {
		t.Fatalf("private comments: %d", res.StatusCode)
	}

	// Shared by code: the gate stands in front of it, on the domain.
	if err := d.Store.SetShareCode(ctx, id, "481523", nil); err != nil {
		t.Fatal(err)
	}
	res, body := fetch(t, domainReq(t, "GET", srv.URL+"/team/", "docs.example.com", ""))
	if res.StatusCode != 200 || body != "gate action=/team/_unlock assets=http://share.test" {
		t.Fatalf("gate: %d %s", res.StatusCode, body)
	}
	req := domainReq(t, "POST", srv.URL+"/team/_unlock", "docs.example.com", "")
	req.Body = io.NopCloser(strings.NewReader("code=000000"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://docs.example.com")
	if res, body := fetch(t, req); res.StatusCode != 200 || !strings.Contains(body, "gate") {
		t.Fatalf("wrong code: %d %s", res.StatusCode, body)
	}
	res, _ = fetch(t, domainReq(t, "GET", srv.URL+"/team/?k=481523", "docs.example.com", ""))
	if res.StatusCode != 303 || res.Header.Get("Location") != "/team/" {
		t.Fatalf("one-click link: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	paths := map[string]bool{}
	var cookies []*http.Cookie
	for _, c := range res.Cookies() {
		paths[c.Path] = true
		cookies = append(cookies, c)
	}
	if !paths["/team/"] || !paths["/c/"+id+"/"] || len(paths) != 2 {
		t.Fatalf("unlock cookie paths: %v", paths)
	}
	open := domainReq(t, "GET", srv.URL+"/team/", "docs.example.com", "")
	open.AddCookie(cookies[0])
	if res, body := fetch(t, open); res.StatusCode != 200 || !strings.Contains(body, "secret") {
		t.Fatalf("unlocked: %d %s", res.StatusCode, body)
	}
	meta := domainReq(t, "GET", srv.URL+"/c/"+id+"/meta", "docs.example.com", "")
	meta.AddCookie(cookies[1])
	if res, _ := fetch(t, meta); res.StatusCode != 200 {
		t.Fatalf("unlocked comments: %d", res.StatusCode)
	}
}

func TestPrimaryDomainRedirect(t *testing.T) {
	srv, d, tok, dns := domainServer(t)
	ctx := context.Background()
	id := publishOne(t, srv.URL, tok, "<p>page</p>")
	connectDomain(t, srv.URL, tok, dns, "docs.example.com")
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "/guide", id); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	share := func(path string, hdr map[string]string) *http.Response {
		req := domainReq(t, "GET", srv.URL+path, "share.test", "")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, _ := fetch(t, req)
		return res
	}
	if res := share("/a/"+id+"/", nil); res.StatusCode != 200 {
		t.Fatalf("before primary: %d", res.StatusCode)
	}
	if code, b := do(t, authJSON(t, "POST", srv.URL+"/api/artifacts/"+id+"/primary-domain", tok, `{"domain":"docs.example.com","path":"/guide"}`)); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	res := share("/a/"+id+"/sub/x.css?q=1", nil)
	if res.StatusCode != 308 || res.Header.Get("Location") != "http://docs.example.com/guide/sub/x.css?q=1" {
		t.Fatalf("redirect: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res.Header.Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("cache: %q", res.Header.Get("Cache-Control"))
	}
	if res := share("/a/"+id+"/", map[string]string{"Sec-Fetch-Dest": "document"}); res.StatusCode != 308 {
		t.Fatalf("navigation: %d", res.StatusCode)
	}
	// The dashboard preview frame and cover images keep loading in place.
	for _, dest := range []string{"iframe", "image"} {
		if res := share("/a/"+id+"/", map[string]string{"Sec-Fetch-Dest": dest}); res.StatusCode != 200 {
			t.Fatalf("%s: %d", dest, res.StatusCode)
		}
	}
	// A private page without a code answers its plain 404, not a redirect
	// that would say where it lives.
	if err := d.Store.SetVisibility(ctx, id, "private"); err != nil {
		t.Fatal(err)
	}
	d.Domains.Index.Invalidate()
	if res := share("/a/"+id+"/", nil); res.StatusCode != 404 {
		t.Fatalf("private: %d", res.StatusCode)
	}
	if err := d.Store.SetVisibility(ctx, id, "public"); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(t, authJSON(t, "POST", srv.URL+"/api/artifacts/"+id+"/primary-domain", tok, `{"domain":null}`)); code != 200 {
		t.Fatal(code)
	}
	if res := share("/a/"+id+"/", nil); res.StatusCode != 200 {
		t.Fatalf("after clearing: %d", res.StatusCode)
	}
}

// Page access control (#74) on a custom domain: the password gate posts
// under the mount, the grant is scoped to the mount, and the form must come
// from the domain itself.
func TestCustomHostAccessControl(t *testing.T) {
	srv, _, tok, dns := domainServer(t)
	id := publishOne(t, srv.URL, tok, "<html><body><p>secret</p></body></html>")
	if code, _ := do(t, authJSON(t, "POST", srv.URL+"/api/domains", tok, `{"domain":"docs.example.com"}`)); code != 201 {
		t.Fatal(code)
	}
	connectDomain(t, srv.URL, tok, dns, "docs.example.com")
	if code, b := mount(t, srv.URL, tok, "docs.example.com", "/team", id); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	if code, a := patchAccess(t, srv.URL, tok, id, `{"mode":"password","password":"hunter2hunter2"}`); code != 200 {
		t.Fatalf("password: %d %v", code, a)
	}
	res, body := fetch(t, domainReq(t, "GET", srv.URL+"/team/", "docs.example.com", ""))
	if res.StatusCode != 200 || body != "gate action=/team/_unlock assets=http://share.test" {
		t.Fatalf("gate: %d %s", res.StatusCode, body)
	}
	unlock := func(origin string) *http.Response {
		req := domainReq(t, "POST", srv.URL+"/team/_unlock", "docs.example.com", "")
		req.Body = io.NopCloser(strings.NewReader("password=hunter2hunter2"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Del("Origin")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, _ := fetch(t, req)
		return res
	}
	for _, o := range []string{"", "https://evil.example", "http://share.test"} {
		if res := unlock(o); res.StatusCode != 403 {
			t.Fatalf("password from %q: %d", o, res.StatusCode)
		}
	}
	res = unlock("http://docs.example.com")
	if res.StatusCode != 303 || res.Header.Get("Location") != "/team/" {
		t.Fatalf("password: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	paths := map[string]bool{}
	var grant *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "artifact_access_"+id {
			paths[c.Path] = true
			grant = c
		}
	}
	if grant == nil || !paths["/team/"] || !paths["/c/"+id+"/"] || len(paths) != 2 {
		t.Fatalf("grant cookies: %v", res.Cookies())
	}
	open := domainReq(t, "GET", srv.URL+"/team/", "docs.example.com", "")
	open.AddCookie(grant)
	if res, body := fetch(t, open); res.StatusCode != 200 || !strings.Contains(body, "secret") {
		t.Fatalf("unlocked: %d %s", res.StatusCode, body)
	}
}
