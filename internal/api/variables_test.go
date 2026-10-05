package api

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/proxy"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

const testSecret = "sk-or-v1-test-0123456789"

var testVariablesKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// upstreamHits records what the fake upstream saw.
type upstreamHits struct {
	n    atomic.Int32
	auth atomic.Value
}

// newVarsServer runs the API with variables on and a proxy that reaches a
// local TLS upstream answering as example.com. adjust may change the config.
func newVarsServer(t *testing.T, adjust func(*Deps)) (*httptest.Server, Deps, string, *upstreamHits) {
	t.Helper()
	hits := &upstreamHits{}
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.n.Add(1)
		hits.auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	t.Cleanup(up.Close)
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())
	_, port, _ := net.SplitHostPort(up.Listener.Addr().String())
	px := proxy.New(proxy.Options{
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		AllowAddr: func(netip.Addr) bool { return true },
		DialPort:  port, RootCAs: pool, Timeout: 3 * time.Second,
	})
	srv, d, tok := newTestServerWith(t, false, func(d *Deps) {
		d.Cfg.VariablesKey = testVariablesKey
		d.Proxy = px
		if adjust != nil {
			adjust(d)
		}
	})
	return srv, d, tok, hits
}

func setVar(t *testing.T, srvURL, tok, name, body string) (int, map[string]any) {
	t.Helper()
	return do(t, jsonReq(t, "PUT", srvURL+"/api/variables/"+name, body, map[string]string{"Authorization": "Bearer " + tok}))
}

const chatManifest = `{"routes":{"chat":{"upstream":"https://example.com/api/v1/chat","methods":["POST"],
  "headers":{"Authorization":"Bearer {{OPENROUTER_API_KEY}}"},"paths":["/models"]}}}`

func publishRoutes(t *testing.T, srvURL, tok, manifest string) (int, map[string]any) {
	t.Helper()
	return do(t, publishReq(t, "POST", srvURL+"/api/artifacts", tok, "Chat", []pubFile{
		{"index.html", "<body>chat</body>"}, {proxy.ManifestPath, manifest}}, nil))
}

func callRoute(t *testing.T, client *http.Client, method, url string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestVariablesAreWriteOnly(t *testing.T) {
	srv, d, tok, _ := newVarsServer(t, nil)
	code, b := setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["https://openrouter.ai/api"]}`)
	if code != 200 || b["created"] != true || b["value"] != nil {
		t.Fatalf("put: %d %v", code, b)
	}
	if ups := b["allowed_upstreams"].([]any); len(ups) != 1 || ups[0] != "openrouter.ai" {
		t.Fatalf("hosts not normalized: %v", ups)
	}
	code, b = setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`-2","allowed_upstreams":["openrouter.ai"]}`)
	if code != 200 || b["created"] != false {
		t.Fatalf("update: %d %v", code, b)
	}
	req := authReq(t, "GET", srv.URL+"/api/variables", tok)
	res, _ := http.DefaultClient.Do(req)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(string(raw), testSecret) || !strings.Contains(string(raw), `"OPENROUTER_API_KEY"`) {
		t.Fatalf("list: %d %s", res.StatusCode, raw)
	}
	// At rest the value is sealed, not stored.
	vs, err := d.Store.Variables(context.Background(), tenantOf(t, d, tok))
	if err != nil || len(vs) != 1 || strings.Contains(vs[0].Sealed, testSecret) || strings.Contains(vs[0].Sealed, base64.RawStdEncoding.EncodeToString([]byte(testSecret))) {
		t.Fatalf("stored: %v %+v", err, vs)
	}
	// Another workspace sees none of it and cannot delete it.
	other := mintKey(t, d, "personal")
	_, ob := do(t, authReq(t, "GET", srv.URL+"/api/variables", other))
	if len(ob["variables"].([]any)) != 0 {
		t.Fatalf("leaked across workspaces: %v", ob)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/variables/OPENROUTER_API_KEY", other)); code != 404 {
		t.Fatalf("cross-workspace delete: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/variables/OPENROUTER_API_KEY", tok)); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/variables/OPENROUTER_API_KEY", tok)); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestVariableValidation(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, nil)
	cases := map[string][2]string{
		"lowercase name":   {"openai", `{"value":"x-123456","allowed_upstreams":["api.openai.com"]}`},
		"no upstreams":     {"KEY", `{"value":"x-123456"}`},
		"empty upstreams":  {"KEY", `{"value":"x-123456","allowed_upstreams":[]}`},
		"ip upstream":      {"KEY", `{"value":"x-123456","allowed_upstreams":["169.254.169.254"]}`},
		"localhost":        {"KEY", `{"value":"x-123456","allowed_upstreams":["localhost"]}`},
		"no value":         {"KEY", `{"allowed_upstreams":["api.openai.com"]}`},
		"crlf value":       {"KEY", `{"value":"a\r\nX-Evil: 1","allowed_upstreams":["api.openai.com"]}`},
		"not json":         {"KEY", `value=1`},
		"too long a value": {"KEY", `{"value":"` + strings.Repeat("a", 4097) + `","allowed_upstreams":["api.openai.com"]}`},
	}
	for name, c := range cases {
		code, b := setVar(t, srv.URL, tok, c[0], c[1])
		if code != 400 {
			t.Errorf("%s: %d %v", name, code, b)
		}
		if msg, _ := b["error"].(map[string]any)["message"].(string); strings.Contains(msg, "X-Evil") || strings.Contains(msg, "x-123456") {
			t.Errorf("%s: error echoes the value: %q", name, msg)
		}
	}
	for i := 0; i < 50; i++ {
		if code, b := setVar(t, srv.URL, tok, "K"+string(rune('A'+i/26))+string(rune('A'+i%26)), `{"value":"v-123456","allowed_upstreams":["a.example.com"]}`); code != 200 {
			t.Fatalf("var %d: %d %v", i, code, b)
		}
	}
	if code, _ := setVar(t, srv.URL, tok, "ONE_TOO_MANY", `{"value":"v-123456","allowed_upstreams":["a.example.com"]}`); code != 429 {
		t.Fatalf("limit: %d", code)
	}
	if code, _ := setVar(t, srv.URL, tok, "KAA", `{"value":"v-654321","allowed_upstreams":["a.example.com"]}`); code != 200 {
		t.Fatalf("updating at the limit must work: %d", code)
	}
}

func TestVariablesOffWithoutKey(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	if code, _ := setVar(t, srv.URL, tok, "KEY", `{"value":"v-123456","allowed_upstreams":["a.example.com"]}`); code != 503 {
		t.Fatalf("put: %d", code)
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/variables", tok)); code != 503 {
		t.Fatalf("list: %d", code)
	}
	code, b := publishRoutes(t, srv.URL, tok, chatManifest)
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	if w := b["proxy"].(map[string]any)["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "off") {
		t.Fatalf("warnings: %v", w)
	}
	if code, _ := callRoute(t, nil, "POST", srv.URL+"/a/"+b["id"].(string)+"/_proxy/chat", nil); code != 404 {
		t.Fatalf("route with the feature off: %d", code)
	}
	if code, _ := setVar(t, srv.URL, tok, "KEY", `{}`); code != 503 {
		t.Fatal("still off")
	}
}

func TestVariablesOffSwitch(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, func(d *Deps) { d.Cfg.VariablesOff = true })
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/variables", tok)); code != 503 {
		t.Fatalf("VARIABLES=off: %d", code)
	}
}

func TestGuestsCannotUseVariablesOrRoutes(t *testing.T) {
	srv, _, _, _ := newVarsServer(t, func(d *Deps) { d.Cfg.GuestPublish = true })
	g := newGuest(t, srv.URL)
	tok := g["token"].(string)
	if code, _ := setVar(t, srv.URL, tok, "KEY", `{"value":"v-123456","allowed_upstreams":["a.example.com"]}`); code != 403 {
		t.Fatalf("guest put: %d", code)
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/variables", tok)); code != 403 {
		t.Fatalf("guest list: %d", code)
	}
	if code, b := publishRoutes(t, srv.URL, tok, chatManifest); code != 403 {
		t.Fatalf("guest publish with routes: %d %v", code, b)
	}
}

func TestPublishValidatesManifest(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, nil)
	for name, man := range map[string]string{
		"broken json":   `{"routes":`,
		"http upstream": `{"routes":{"x":{"upstream":"http://example.com","methods":["GET"]}}}`,
		"self":          `{"routes":{"x":{"upstream":"https://api.test/api/me","methods":["GET"]}}}`,
		"metadata ip":   `{"routes":{"x":{"upstream":"https://169.254.169.254/latest/meta-data","methods":["GET"]}}}`,
	} {
		if code, b := publishRoutes(t, srv.URL, tok, man); code != 400 || !strings.Contains(b["error"].(map[string]any)["message"].(string), proxy.ManifestPath) {
			t.Errorf("%s: %d %v", name, code, b)
		}
	}
	code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "x"}, {"_proxy/chat", "shadow"}}, nil))
	if code != 400 {
		t.Fatalf("_proxy/ file: %d %v", code, b)
	}
	code, b = publishRoutes(t, srv.URL, tok, chatManifest)
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	info := b["proxy"].(map[string]any)
	if r := info["routes"].([]any); len(r) != 1 || r[0] != "chat" {
		t.Fatalf("routes: %v", info)
	}
	if w := info["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "OPENROUTER_API_KEY") {
		t.Fatalf("warnings: %v", info)
	}
	// The manifest is configuration, never content.
	id := b["id"].(string)
	for _, p := range []string{"/a/" + id + "/" + proxy.ManifestPath, "/a/" + id + "/v/1/" + proxy.ManifestPath} {
		res, _ := http.Get(srv.URL + p)
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("%s served: %d", p, res.StatusCode)
		}
	}
}

func TestProxyRouteEndToEnd(t *testing.T) {
	srv, d, tok, hits := newVarsServer(t, nil)
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	code, b := publishRoutes(t, srv.URL, tok, chatManifest)
	if code != 201 || len(b["proxy"].(map[string]any)["warnings"].([]any)) != 0 {
		t.Fatalf("publish: %d %v", code, b)
	}
	id := b["id"].(string)
	base := srv.URL + "/a/" + id + "/_proxy/chat"

	code, body := callRoute(t, nil, "POST", base, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if code != 200 || hits.auth.Load() != "Bearer "+testSecret || !strings.Contains(body, `"/api/v1/chat"`) {
		t.Fatalf("call: %d %s auth=%v", code, body, hits.auth.Load())
	}
	if strings.Contains(body, testSecret) {
		t.Fatal("secret reached the browser")
	}
	if code, body = callRoute(t, nil, "POST", base+"/models", nil); code != 200 || !strings.Contains(body, "/api/v1/chat/models") {
		t.Fatalf("declared sub-path: %d %s", code, body)
	}
	before := hits.n.Load()
	for name, c := range map[string]struct {
		method, url string
		hdr         map[string]string
		want        int
	}{
		"undeclared method":   {"GET", base, nil, 405},
		"undeclared sub-path": {"POST", base + "/admin", nil, 404},
		"encoded traversal":   {"POST", base + "/..%2Fadmin", nil, 404},
		"unknown route":       {"POST", srv.URL + "/a/" + id + "/_proxy/other", nil, 404},
		"cross-site browser":  {"POST", base, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		"same-site browser":   {"POST", base, map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		"foreign origin":      {"POST", base, map[string]string{"Origin": "https://evil.example"}, 403},
		"sandboxed origin":    {"POST", base, map[string]string{"Origin": "null"}, 403},
		"loop":                {"POST", base, map[string]string{proxy.LoopHeader: "1"}, 403},
		"no such page":        {"POST", srv.URL + "/a/nope/_proxy/chat", nil, 404},
	} {
		if code, body := callRoute(t, nil, c.method, c.url, c.hdr); code != c.want {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if hits.n.Load() != before {
		t.Fatalf("a refused call reached the upstream (%d calls)", hits.n.Load()-before)
	}
	// Pinning: the variable no longer lists the route's host.
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["openrouter.ai"]}`)
	// The visitor is told the route is unavailable, never which variable
	// or host is at fault.
	if code, body := callRoute(t, nil, "POST", base, nil); code != 502 || strings.Contains(body, testSecret) ||
		strings.Contains(body, "OPENROUTER_API_KEY") || strings.Contains(body, "example.com") || hits.n.Load() != before {
		t.Fatalf("unpinned host: %d %s", code, body)
	}
	// A variable of the same name in another workspace is never used.
	other := mintKey(t, d, "personal")
	_, ob := publishRoutes(t, srv.URL, other, chatManifest)
	if code, body := callRoute(t, nil, "POST", srv.URL+"/a/"+ob["id"].(string)+"/_proxy/chat", nil); code != 502 || hits.n.Load() != before ||
		strings.Contains(body, "OPENROUTER_API_KEY") {
		t.Fatalf("another workspace's page used this workspace's variable: %d %s", code, body)
	}
	// Deleting the page closes its routes.
	do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, tok))
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 410 && code != 404 {
		t.Fatalf("deleted page: %d", code)
	}
}

func TestProxyReadsLiveVersionOnly(t *testing.T) {
	srv, _, tok, hits := newVarsServer(t, nil)
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, srv.URL, tok, chatManifest)
	id := b["id"].(string)
	base := srv.URL + "/a/" + id + "/_proxy/chat"
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 200 {
		t.Fatal(code)
	}
	// Version 2 drops the manifest: the route is gone, version 1 or not.
	code, _ := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "<body>v2</body>"}}, nil))
	if code != 200 {
		t.Fatal(code)
	}
	n := hits.n.Load()
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 404 || hits.n.Load() != n {
		t.Fatalf("route outlived its manifest: %d", code)
	}
	if code, _ := callRoute(t, nil, "POST", srv.URL+"/a/"+id+"/v/1/_proxy/chat", nil); code == 200 {
		t.Fatal("a pinned old version still runs routes")
	}
}

func TestProxyRateLimits(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, func(d *Deps) {
		d.Cfg.Limits.ProxyPerIPHour = 3
		d.Cfg.Limits.ProxyPerPageHour = 1000
	})
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, srv.URL, tok, chatManifest)
	base := srv.URL + "/a/" + b["id"].(string) + "/_proxy/chat"
	for i := 0; i < 3; i++ {
		if code, _ := callRoute(t, nil, "POST", base, nil); code != 200 {
			t.Fatalf("call %d: %d", i, code)
		}
	}
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 429 {
		t.Fatalf("per-IP limit: %d", code)
	}

	srv2, _, tok2, _ := newVarsServer(t, func(d *Deps) {
		d.Cfg.Limits.ProxyPerIPHour = 1000
		d.Cfg.Limits.ProxyPerPageHour = 2
	})
	setVar(t, srv2.URL, tok2, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b1 := publishRoutes(t, srv2.URL, tok2, chatManifest)
	_, b2 := publishRoutes(t, srv2.URL, tok2, chatManifest)
	for i := 0; i < 2; i++ {
		callRoute(t, nil, "POST", srv2.URL+"/a/"+b1["id"].(string)+"/_proxy/chat", nil)
	}
	if code, _ := callRoute(t, nil, "POST", srv2.URL+"/a/"+b1["id"].(string)+"/_proxy/chat", nil); code != 429 {
		t.Fatalf("per-page limit: %d", code)
	}
	if code, _ := callRoute(t, nil, "POST", srv2.URL+"/a/"+b2["id"].(string)+"/_proxy/chat", nil); code != 200 {
		t.Fatalf("another page has its own budget: %d", code)
	}
}

// A private page's routes answer only a browser that has unlocked the page
// with its share code: the same gate as the page itself.
func TestProxyPrivatePageNeedsUnlock(t *testing.T) {
	srv, _, tok, hits := newVarsServer(t, nil)
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	code, b := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"visibility": "private"},
		[]pubFile{{"index.html", "<body>private</body>"}, {proxy.ManifestPath, chatManifest}}))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	id := b["id"].(string)
	base := srv.URL + "/a/" + id + "/_proxy/chat"
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 404 || hits.n.Load() != 0 {
		t.Fatalf("private page without unlock: %d", code)
	}
	_, sc := do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/share-code", `{"action":"issue"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 404 {
		t.Fatalf("code issued but not presented: %d", code)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	res, err := client.Get(srv.URL + "/a/" + id + "/?k=" + sc["share_code"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if code, body := callRoute(t, client, "POST", base, nil); code != 200 {
		t.Fatalf("unlocked browser: %d %s", code, body)
	}
}

// Probing routes that do not exist, or methods a route does not take, must
// not spend the page's budget; only calls that reach a route do.
func TestProxyPageBudgetOnlyForRealRoutes(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, func(d *Deps) {
		d.Cfg.Limits.ProxyPerIPHour = 1000
		d.Cfg.Limits.ProxyPerPageHour = 2
	})
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, srv.URL, tok, chatManifest)
	page := srv.URL + "/a/" + b["id"].(string) + "/_proxy/"
	for i := 0; i < 20; i++ {
		callRoute(t, nil, "POST", page+"nonexistent", nil)
		callRoute(t, nil, "GET", page+"chat", nil)
		callRoute(t, nil, "POST", page+"chat/undeclared", nil)
	}
	for i := 0; i < 2; i++ {
		if code, body := callRoute(t, nil, "POST", page+"chat", nil); code != 200 {
			t.Fatalf("call %d after probing: %d %s", i, code, body)
		}
	}
	if code, _ := callRoute(t, nil, "POST", page+"chat", nil); code != 429 {
		t.Fatalf("budget: %d", code)
	}
}

func TestProxyOwnerPageLimit(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, nil)
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	man := `{"limit_per_hour":2,"routes":{"chat":{"upstream":"https://example.com/api/v1/chat","methods":["POST"],
	  "headers":{"Authorization":"Bearer {{OPENROUTER_API_KEY}}"}}}}`
	_, b := publishRoutes(t, srv.URL, tok, man)
	base := srv.URL + "/a/" + b["id"].(string) + "/_proxy/chat"
	for i := 0; i < 2; i++ {
		if code, _ := callRoute(t, nil, "POST", base, nil); code != 200 {
			t.Fatalf("call %d: %d", i, code)
		}
	}
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 429 {
		t.Fatalf("owner limit: %d", code)
	}
}

// An IPv6 client is limited by its /64, so cycling through addresses in its
// own prefix does not buy more calls.
func TestProxyIPv6BucketsBySlash64(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, func(d *Deps) {
		d.Cfg.TrustProxyHeaders = true
		d.Cfg.Limits.ProxyPerIPHour = 2
	})
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, srv.URL, tok, chatManifest)
	base := srv.URL + "/a/" + b["id"].(string) + "/_proxy/chat"
	for i, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2"} {
		if code, _ := callRoute(t, nil, "POST", base, map[string]string{"CF-Connecting-IP": ip}); code != 200 {
			t.Fatalf("call %d: %d", i, code)
		}
	}
	if code, _ := callRoute(t, nil, "POST", base, map[string]string{"CF-Connecting-IP": "2001:db8:1:2:ffff::9"}); code != 429 {
		t.Fatalf("same /64, fresh address: %d", code)
	}
	if code, _ := callRoute(t, nil, "POST", base, map[string]string{"CF-Connecting-IP": "2001:db8:1:3::1"}); code != 200 {
		t.Fatalf("another /64: %d", code)
	}
}

func TestProxyEndUserPublishNeedsOptIn(t *testing.T) {
	srv, d, _, _ := newVarsServer(t, nil)
	tok := mintKey(t, d, "integrator")
	files := []pubFile{{"index.html", "<body>customer</body>"}, {proxy.ManifestPath, chatManifest}}
	req := publishWith(t, "POST", srv.URL+"/api/artifacts", tok, nil, files)
	req.Header.Set("X-Artifact-End-User", "customer-1")
	if code, b := do(t, req); code != 403 || !strings.Contains(b["error"].(map[string]any)["message"].(string), "proxy_routes_for_end_user") {
		t.Fatalf("end-user publish with routes: %d %v", code, b)
	}
	req = publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"proxy_routes_for_end_user": true}, files)
	req.Header.Set("X-Artifact-End-User", "customer-1")
	if code, b := do(t, req); code != 201 || b["proxy"] == nil {
		t.Fatalf("opted in: %d %v", code, b)
	}
}

// A version whose manifest never passed the publish checks (added before
// routes existed, by a guest key later claimed, or for an end user without
// the opt-in) runs no routes, also when a restore makes it live again.
func TestProxyOnlyVettedVersionsRunRoutes(t *testing.T) {
	srv, d, tok, _ := newVarsServer(t, nil)
	setVar(t, srv.URL, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, srv.URL, tok, chatManifest)
	id := b["id"].(string)
	base := srv.URL + "/a/" + id + "/_proxy/chat"
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 200 {
		t.Fatal(code)
	}
	ctx := context.Background()
	files, err := d.Store.Files(ctx, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Store.AddVersion(ctx, store.Version{ArtifactID: id, Version: 2, CreatedAt: d.Now(), FileCount: len(files)}, files); err != nil {
		t.Fatal(err)
	}
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 404 {
		t.Fatalf("unvetted live version ran routes: %d", code)
	}
	code, rb := do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/versions/1/restore", "", map[string]string{"Authorization": "Bearer " + tok}))
	if code != 200 {
		t.Fatalf("restore: %d %v", code, rb)
	}
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 200 {
		t.Fatalf("restored vetted version: %d", code)
	}
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/versions/2/restore", "", map[string]string{"Authorization": "Bearer " + tok}))
	if code != 200 {
		t.Fatal(code)
	}
	if code, _ := callRoute(t, nil, "POST", base, nil); code != 404 {
		t.Fatalf("restoring an unvetted version switched routes on: %d", code)
	}
}

func TestCoverCannotBeAHiddenFile(t *testing.T) {
	srv, _, tok, _ := newVarsServer(t, nil)
	code, b := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"cover": ".byagent/cover.svg"},
		[]pubFile{{"index.html", "x"}, {".byagent/cover.svg", "<svg/>"}}))
	if code != 400 {
		t.Fatalf("hidden cover: %d %v", code, b)
	}
}

// Routes of a password page answer only browsers holding the page's grant,
// and changing the password cuts them off with the page.
func TestProxyPasswordPageNeedsItsGrant(t *testing.T) {
	srv, _, tok, hits := newVarsServer(t, nil)
	base := srv.URL
	setVar(t, base, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, base, tok, chatManifest)
	id := b["id"].(string)
	route := base + "/a/" + id + "/_proxy/chat"
	if code, a := patchAccess(t, base, tok, id, `{"mode":"password","password":"hunter2hunter2"}`); code != 200 {
		t.Fatalf("%d %v", code, a)
	}
	if code, _ := callRoute(t, nil, "POST", route, nil); code != 404 || hits.n.Load() != 0 {
		t.Fatalf("no grant: %d", code)
	}
	unlock := formPost(t, base+"/a/"+id+"/_unlock", url.Values{"password": {"hunter2hunter2"}})
	c := grantCookie(t, unlock, id)
	withGrant := func() int {
		req, _ := http.NewRequest("POST", route, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := withGrant(); code != 200 {
		t.Fatalf("with the grant: %d", code)
	}
	forged := *c
	forged.Value = c.Value[:len(c.Value)-4] + "0000"
	req, _ := http.NewRequest("POST", route, nil)
	req.AddCookie(&forged)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 404 {
		t.Fatalf("forged grant: %d", res.StatusCode)
	}
	if code, _ := patchAccess(t, base, tok, id, `{"mode":"password","password":"another-password-1"}`); code != 200 {
		t.Fatal(code)
	}
	if code := withGrant(); code != 404 {
		t.Fatalf("grant outlived the password: %d", code)
	}
}

// Routes of a restricted page answer only a verified address still on the
// allowlist.
func TestProxyRestrictedPageNeedsAnAllowedAddress(t *testing.T) {
	mail := &captureMail{}
	srv, _, tok, hits := newVarsServer(t, func(d *Deps) {
		d.Mail = mail
		d.WebGate = func(w http.ResponseWriter, r *http.Request, g serve.GateView) { w.WriteHeader(200) }
	})
	base := srv.URL
	setVar(t, base, tok, "OPENROUTER_API_KEY", `{"value":"`+testSecret+`","allowed_upstreams":["example.com"]}`)
	_, b := publishRoutes(t, base, tok, chatManifest)
	id := b["id"].(string)
	route := base + "/a/" + id + "/_proxy/chat"
	if code, a := patchAccess(t, base, tok, id, `{"mode":"restricted","allowed_domains":["corp.example"]}`); code != 200 {
		t.Fatalf("%d %v", code, a)
	}
	if code, _ := callRoute(t, nil, "POST", route, nil); code != 404 || hits.n.Load() != 0 {
		t.Fatalf("no grant: %d", code)
	}
	formPost(t, base+"/a/"+id+"/_access/email", url.Values{"email": {"lee@corp.example"}})
	_, text := mail.waitMail(t, 1)
	m := mailCodeRe.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("mail: %q", text)
	}
	c := grantCookie(t, formPost(t, base+"/a/"+id+"/_access/code", url.Values{"email": {"lee@corp.example"}, "code": {m[1]}}), id)
	withGrant := func() int {
		req, _ := http.NewRequest("POST", route, strings.NewReader(`{}`))
		req.AddCookie(c)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := withGrant(); code != 200 {
		t.Fatalf("allowed address: %d", code)
	}
	if code, _ := patchAccess(t, base, tok, id, `{"mode":"restricted","allowed_emails":["sam@acme.com"]}`); code != 200 {
		t.Fatal(code)
	}
	if code := withGrant(); code != 404 {
		t.Fatalf("taken off the list, still calling routes: %d", code)
	}
}
