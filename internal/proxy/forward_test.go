package proxy

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secret = "sk-live-0123456789abcdef"

// upstream is a TLS server answering as example.com, which is a name its
// test certificate covers, so the proxy verifies it like a real host.
func upstream(t *testing.T, h http.HandlerFunc) (*Proxy, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p := New(Options{
		Resolve:   fixedResolve("127.0.0.1"),
		AllowAddr: func(netip.Addr) bool { return true },
		DialPort:  port,
		RootCAs:   pool,
		Timeout:   2 * time.Second,
	})
	return p, srv
}

func route(t *testing.T, js string) Route {
	t.Helper()
	m, err := Parse([]byte(`{"routes":{"r":` + js + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	return m.Routes["r"]
}

func call(p *Proxy, rt Route, req *http.Request, sub string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	p.Forward(rec, req, Call{Route: rt, Sub: sub, Secrets: map[string]string{"KEY": secret}, Label: "test/r"})
	return rec
}

func TestForwardInjectsSecretAndStripsVisitorHeaders(t *testing.T) {
	var got *http.Request
	var body []byte
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=upstream; Path=/")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Clear-Site-Data", `"*"`)
		w.Header().Set("Strict-Transport-Security", "max-age=1")
		w.Header().Set("X-Ratelimit-Remaining", "41")
		w.Write([]byte(`{"ok":true}`))
	})
	rt := route(t, `{"upstream":"https://example.com/v1/chat?fixed=1","methods":["POST"],
		"headers":{"Authorization":"Bearer {{KEY}}"},"query":{"key":"{{KEY}}"}}`)
	req := httptest.NewRequest("POST", "/a/x/_proxy/r?q=hi&key=visitor&fixed=2", strings.NewReader(`{"m":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cookie", "artifact_unlock_x=abc")
	req.Header.Set("Authorization", "Bearer visitor-token")
	req.Header.Set("Referer", "https://byagent.dev/a/x/")
	req.Header.Set("Origin", "https://byagent.dev")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Custom", "nope")
	rec := call(p, rt, req, "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Header.Get("Authorization") != "Bearer "+secret {
		t.Fatalf("secret not injected: %q", got.Header.Get("Authorization"))
	}
	for _, h := range []string{"Cookie", "Referer", "Origin", "X-Forwarded-For", "X-Custom"} {
		if got.Header.Get(h) != "" {
			t.Errorf("visitor header %s reached the upstream", h)
		}
	}
	if got.Header.Get("Content-Type") != "application/json" || got.Header.Get("Accept") != "text/event-stream" {
		t.Error("content negotiation headers must pass")
	}
	if got.Header.Get(LoopHeader) == "" {
		t.Error("loop marker missing")
	}
	if string(body) != `{"m":1}` || got.URL.Path != "/v1/chat" {
		t.Errorf("body %q path %q", body, got.URL.Path)
	}
	q := got.URL.Query()
	if q.Get("key") != secret || len(q["key"]) != 1 || q.Get("fixed") != "1" || len(q["fixed"]) != 1 || q.Get("q") != "hi" {
		t.Errorf("query: visitor overrode or repeated route values: %v", q)
	}
	for _, h := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "Clear-Site-Data", "Strict-Transport-Security"} {
		if rec.Header().Get(h) != "" {
			t.Errorf("upstream header %s reached the page origin", h)
		}
	}
	if rec.Header().Get("X-Ratelimit-Remaining") != "41" || rec.Header().Get("Content-Type") != "application/json" {
		t.Error("useful upstream headers must pass")
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Error("proxied answers must be no-store and sandboxed")
	}
}

func TestForwardSubPath(t *testing.T) {
	var path string
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) { path = r.URL.EscapedPath() })
	rt := route(t, `{"upstream":"https://example.com/rest/v1/","methods":["GET"],"paths":["/items/*"]}`)
	if rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "/items/a%20b"); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if path != "/rest/v1/items/a%20b" {
		t.Fatalf("path %q", path)
	}
}

func TestForwardDoesNotFollowRedirects(t *testing.T) {
	var hops atomic.Int32
	p, srv := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
			return
		}
		w.Write([]byte("followed"))
	})
	_ = srv
	rt := route(t, `{"upstream":"https://example.com/start","methods":["GET"],"headers":{"Authorization":"Bearer {{KEY}}"}}`)
	rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "")
	if rec.Code != http.StatusBadGateway || hops.Load() != 1 {
		t.Fatalf("redirect followed: %d hops=%d", rec.Code, hops.Load())
	}
	if rec.Header().Get("Location") != "" {
		t.Fatal("Location leaked")
	}
}

func TestForwardRedactsEchoedSecret(t *testing.T) {
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ratelimit-Echo", r.Header.Get("Authorization"))
		// Split the secret across two flushed writes.
		auth := r.Header.Get("Authorization")
		w.Write([]byte(`{"headers":{"Authorization":"` + auth[:15]))
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		w.Write([]byte(auth[15:] + `"}}`))
	})
	rt := route(t, `{"upstream":"https://example.com/anything","methods":["GET"],"headers":{"Authorization":"Bearer {{KEY}}"}}`)
	rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "")
	if strings.Contains(rec.Body.String(), secret) || !strings.Contains(rec.Body.String(), redactedMark) {
		t.Fatalf("secret echoed: %s", rec.Body)
	}
	if strings.Contains(rec.Header().Get("X-Ratelimit-Echo"), secret) {
		t.Fatal("secret echoed in a header")
	}
}

func TestRedactorHoldsOnlyPossiblePrefixes(t *testing.T) {
	var out bytes.Buffer
	r := newRedactor(&out, map[string]string{"K": secret, "SHORT": "abc"})
	r.Write([]byte("data: one\n\n"))
	if out.String() != "data: one\n\n" {
		t.Fatalf("ordinary text held back: %q", out.String())
	}
	r.Write([]byte("x sk-live-01"))
	if strings.HasSuffix(out.String(), "sk-live-01") {
		t.Fatal("a possible secret prefix was written early")
	}
	r.Write([]byte("23456789abcdef y abc"))
	r.Flush()
	if strings.Contains(out.String(), secret) || !strings.Contains(out.String(), "x [redacted] y abc") {
		t.Fatalf("got %q", out.String())
	}
}

func TestForwardStreamsEvents(t *testing.T) {
	release := make(chan struct{})
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("data: second\n\n"))
	})
	rt := route(t, `{"upstream":"https://example.com/stream","methods":["GET"]}`)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.Forward(w, r, Call{Route: rt, Label: "t"})
	}))
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "first") {
		t.Fatalf("first event not streamed: %q", buf[:n])
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(rest), "second") {
		t.Fatalf("rest: %q", rest)
	}
}

func TestForwardCapsResponses(t *testing.T) {
	big := bytes.Repeat([]byte("a"), MaxResponseBytes+10)
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/declared" {
			w.Header().Set("Content-Length", "11000000")
		}
		w.Write(big)
	})
	rt := route(t, `{"upstream":"https://example.com/declared","methods":["GET"]}`)
	if rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("declared oversize: %d", rec.Code)
	}
	rt = route(t, `{"upstream":"https://example.com/chunked","methods":["GET"]}`)
	rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "")
	if rec.Body.Len() > MaxResponseBytes {
		t.Fatalf("streamed %d bytes past the cap", rec.Body.Len())
	}
}

func TestForwardCapsRequestBodies(t *testing.T) {
	var hit atomic.Bool
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) { hit.Store(true) })
	rt := route(t, `{"upstream":"https://example.com/x","methods":["POST"]}`)
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(make([]byte, MaxRequestBytes+1)))
	if rec := call(p, rt, req, ""); rec.Code != http.StatusRequestEntityTooLarge || hit.Load() {
		t.Fatalf("oversize body: %d hit=%v", rec.Code, hit.Load())
	}
}

func TestForwardTimesOut(t *testing.T) {
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	rt := route(t, `{"upstream":"https://example.com/slow","methods":["GET"]}`)
	if rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), ""); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("slow upstream: %d", rec.Code)
	}
}

// With the production address check, a host that resolves to a private or
// metadata address is refused, and neither the answer nor the log names the
// secret, though the URL carried it in its query.
func TestForwardBlocksPrivateUpstreamWithoutLeaking(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	for _, addr := range []string{"127.0.0.1", "169.254.169.254", "10.1.2.3", "::1"} {
		p := New(Options{Resolve: fixedResolve(addr), Timeout: time.Second})
		rt := route(t, `{"upstream":"https://rebind.example.com/x","methods":["GET"],"query":{"key":"{{KEY}}"}}`)
		rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d", addr, rec.Code)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s: secret in the answer", addr)
		}
	}
	// An unreachable host errors the same way: generic text, no URL.
	p := New(Options{Resolve: fixedResolve("127.0.0.1"), AllowAddr: func(netip.Addr) bool { return true }, DialPort: "1", Timeout: time.Second})
	rt := route(t, `{"upstream":"https://down.example.com/x","methods":["GET"],"query":{"key":"{{KEY}}"}}`)
	rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("unreachable: %d %s", rec.Code, rec.Body)
	}
	var env struct {
		Error struct{ Message string } `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &env)
	if strings.Contains(env.Error.Message, "down.example.com") {
		t.Errorf("error names the URL: %q", env.Error.Message)
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), url.QueryEscape(secret)) {
		t.Fatalf("secret in logs: %s", logs.String())
	}
}

func TestResolveChecksPinning(t *testing.T) {
	rt := route(t, `{"upstream":"https://api.openai.com/v1/x","methods":["GET"],"headers":{"Authorization":"Bearer {{KEY}}"}}`)
	if _, e := rt.Resolve(map[string]Secret{}); e == nil || e.Status != 502 {
		t.Fatalf("missing variable: %v", e)
	}
	if _, e := rt.Resolve(map[string]Secret{"KEY": {Value: secret, AllowedUpstreams: []string{"openrouter.ai"}}}); e == nil || e.Status != 403 || strings.Contains(e.Message, secret) {
		t.Fatalf("unpinned host: %v", e)
	}
	if _, e := rt.Resolve(map[string]Secret{"KEY": {Value: secret, AllowedUpstreams: []string{"evilopenai.com"}}}); e == nil {
		t.Fatal("suffix trick accepted")
	}
	vals, e := rt.Resolve(map[string]Secret{"KEY": {Value: secret, AllowedUpstreams: []string{"openai.com"}}})
	if e != nil || vals["KEY"] != secret {
		t.Fatalf("parent domain pin: %v", e)
	}
}
