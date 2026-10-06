package proxy

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
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

	"github.com/anup-a/artifacts/internal/apierr"
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
	if rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "/items/a-b_c.d~e"); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if path != "/rest/v1/items/a-b_c.d~e" {
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
		// Stay silent past the deadline: returning the moment the request is
		// cancelled can race an empty 200 back ahead of the cancellation.
		select {
		case <-r.Context().Done():
			time.Sleep(500 * time.Millisecond)
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
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "route unavailable") {
			t.Errorf("%s: status %d %s", addr, rec.Code, rec.Body)
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
	if _, err := rt.Resolve(map[string]Secret{}); err == nil || !strings.Contains(err.Error(), "KEY") {
		t.Fatalf("missing variable: %v", err)
	}
	if _, err := rt.Resolve(map[string]Secret{"KEY": {Value: secret, AllowedUpstreams: []string{"openrouter.ai"}}}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unpinned host: %v", err)
	}
	if _, err := rt.Resolve(map[string]Secret{"KEY": {Value: secret, AllowedUpstreams: []string{"evilopenai.com"}}}); err == nil {
		t.Fatal("suffix trick accepted")
	}
	if _, err := rt.Resolve(map[string]Secret{"KEY": {Value: strings.Repeat("k", MaxSecretBytes+1), AllowedUpstreams: []string{"openai.com"}}}); err == nil {
		t.Fatal("oversized secrets accepted")
	}
	vals, err := rt.Resolve(map[string]Secret{"KEY": {Value: secret, AllowedUpstreams: []string{"openai.com"}}})
	if err != nil || vals["KEY"] != secret {
		t.Fatalf("parent domain pin: %v", err)
	}
}

// A worst case for naive matching: long secrets that are all one byte but
// the last, against a body of nothing but that byte. Matching must stay
// linear in the body.
func TestRedactorIsLinear(t *testing.T) {
	run := func(fill string) time.Duration {
		secrets := map[string]string{}
		for i := 0; i < MaxVarsPerRoute; i++ {
			secrets[fmt.Sprint("K", i)] = strings.Repeat(fill, 2047) + string(rune('B'+i))
		}
		r := newRedactor(io.Discard, secrets)
		chunk := bytes.Repeat([]byte("A"), 32<<10)
		start := time.Now()
		for n := 0; n < MaxResponseBytes; n += len(chunk) {
			r.Write(chunk)
		}
		return time.Since(start)
	}
	benign := run("Z")
	hostile := run("A")
	// Quadratic matching would make the hostile case thousands of times
	// slower than the benign one; linear keeps them in the same range. The
	// ratio, not wall time, is checked so a busy machine does not fail it.
	if hostile > 20*benign+500*time.Millisecond {
		t.Fatalf("hostile %v vs benign %v", hostile, benign)
	}
}

func TestRedactorAcrossManyChunks(t *testing.T) {
	var out bytes.Buffer
	r := newRedactor(&out, map[string]string{"K": "abcabcabd-secret"})
	in := "xx abcabcabcabd-secret yy abcabcabd-secre"
	for i := 0; i < len(in); i++ {
		r.Write([]byte{in[i]})
	}
	r.Flush()
	if got := out.String(); got != "xx abc[redacted] yy abcabcabd-secre" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactorNeverFlushesACutSecret(t *testing.T) {
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), MaxResponseBytes-10))
		w.Write([]byte(secret))
	})
	rt := route(t, `{"upstream":"https://example.com/big","methods":["GET"],"headers":{"Authorization":"Bearer {{KEY}}"}}`)
	rec := call(p, rt, httptest.NewRequest("GET", "/x", nil), "")
	if strings.Contains(rec.Body.String(), secret[:10]) {
		t.Fatal("the start of a secret cut off by the cap was sent")
	}
}

func TestForwardChunkedRequestBodies(t *testing.T) {
	var got atomic.Int64
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		got.Store(n)
	})
	rt := route(t, `{"upstream":"https://example.com/up","methods":["POST"]}`)
	// Unknown length: read through the cap, refused once past it.
	req := httptest.NewRequest("POST", "/x", io.MultiReader(bytes.NewReader(make([]byte, MaxRequestBytes)), strings.NewReader("over")))
	req.ContentLength = -1
	if rec := call(p, rt, req, ""); rec.Code == 200 {
		t.Fatalf("an oversized chunked body went through (%d bytes)", got.Load())
	}
	req = httptest.NewRequest("POST", "/x", bytes.NewReader(make([]byte, 1<<20)))
	req.ContentLength = -1
	if rec := call(p, rt, req, ""); rec.Code != 200 || got.Load() != 1<<20 {
		t.Fatalf("chunked body: %d, %d bytes", rec.Code, got.Load())
	}
}

func TestForwardCapsConcurrency(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	p, srv := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
	})
	defer close(release)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	p = New(Options{Resolve: fixedResolve("127.0.0.1"), AllowAddr: func(netip.Addr) bool { return true }, DialPort: port,
		RootCAs: pool, Timeout: 5 * time.Second, InFlight: 3, InFlightPerTenant: 2})
	rt := route(t, `{"upstream":"https://example.com/slow","methods":["GET"]}`)
	callAs := func(tenant string) chan int {
		ch := make(chan int, 1)
		go func() {
			rec := httptest.NewRecorder()
			p.Forward(rec, httptest.NewRequest("GET", "/x", nil), Call{Route: rt, Tenant: tenant, Label: "t"})
			ch <- rec.Code
		}()
		return ch
	}
	callAs("a")
	callAs("a")
	<-started
	<-started
	if code := <-callAs("a"); code != http.StatusServiceUnavailable {
		t.Fatalf("third call for one workspace: %d", code)
	}
	callAs("b")
	<-started
	if code := <-callAs("c"); code != http.StatusServiceUnavailable {
		t.Fatalf("call past the server cap: %d", code)
	}
}

func TestForwardAppliesBodyRules(t *testing.T) {
	var got []byte
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) { got, _ = io.ReadAll(r.Body) })
	rt := route(t, `{"upstream":"https://example.com/chat","methods":["POST"],
		"body":{"pin":{"model":"cheap/model"},"max":{"max_tokens":512}}}`)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"model":"expensive/model","max_tokens":100000,"messages":[]}`))
	if rec := call(p, rt, req, ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	var body map[string]any
	json.Unmarshal(got, &body)
	if body["model"] != "cheap/model" || body["max_tokens"] != 512.0 || body["messages"] == nil {
		t.Fatalf("body sent: %s", got)
	}
	req = httptest.NewRequest("POST", "/x", strings.NewReader(`{"messages":[]}`))
	call(p, rt, req, "")
	json.Unmarshal(got, &body)
	if body["max_tokens"] != 512.0 {
		t.Fatalf("missing cap not filled in: %s", got)
	}
	got = nil
	for _, bad := range []string{`[1,2]`, `not json`, `{"max_tokens":"lots"}`} {
		if rec := call(p, rt, httptest.NewRequest("POST", "/x", strings.NewReader(bad)), ""); rec.Code != 400 || got != nil {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	big := `{"x":"` + strings.Repeat("a", MaxRuleBodyBytes) + `"}`
	if rec := call(p, rt, httptest.NewRequest("POST", "/x", strings.NewReader(big)), ""); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized rule body: %d", rec.Code)
	}
}

// slowProxy is upstream with its own options: a TLS server answering as
// example.com and its subdomains, and a proxy built with o.
func slowProxy(t *testing.T, o Options, h http.HandlerFunc) *Proxy {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	o.Resolve, o.AllowAddr, o.DialPort, o.RootCAs = fixedResolve("127.0.0.1"), func(netip.Addr) bool { return true }, port, pool
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	return New(o)
}

func TestForwardReadsBodyBeforeTakingASlot(t *testing.T) {
	p := slowProxy(t, Options{InFlight: 1, BodyTimeout: 300 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
	})
	rt := route(t, `{"upstream":"https://example.com/up","methods":["POST"]}`)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.Forward(w, r, Call{Route: rt, Tenant: r.URL.Query().Get("t"), Label: "t"})
	}))
	defer front.Close()
	// A client that promises 100 bytes and sends 5, then stalls.
	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	fmt.Fprintf(conn, "POST /?t=slow HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nhello")
	time.Sleep(50 * time.Millisecond)
	// The only slot is free while the slow body trickles in.
	res, err := http.Post(front.URL+"/?t=fast", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("a call while another body was being read: %d", res.StatusCode)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, _ := io.ReadAll(io.LimitReader(conn, 12))
	if !strings.HasPrefix(string(line), "HTTP/1.1 408") {
		t.Fatalf("stalled body answered %q", line)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("stalled body held for %v", d)
	}
}

func TestForwardCapsPerUpstreamSite(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	p := slowProxy(t, Options{InFlight: 10, InFlightPerTenant: 10, InFlightPerHost: 2}, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
	})
	defer close(release)
	callTo := func(host, tenant string) chan int {
		rt := route(t, `{"upstream":"https://`+host+`/slow","methods":["GET"]}`)
		ch := make(chan int, 1)
		go func() {
			rec := httptest.NewRecorder()
			p.Forward(rec, httptest.NewRequest("GET", "/x", nil), Call{Route: rt, Tenant: tenant, Label: "t"})
			ch <- rec.Code
		}()
		return ch
	}
	callTo("a.example.com", "t1")
	callTo("b.example.com", "t2")
	<-started
	<-started
	// Another subdomain of the same site, from another workspace, is held
	// by the site's cap.
	if code := <-callTo("c.example.com", "t3"); code != http.StatusServiceUnavailable {
		t.Fatalf("third call to one site: %d", code)
	}
	if siteOf("API.OpenAI.com.") != "openai.com" || siteOf("x.github.io") != "x.github.io" {
		t.Fatalf("siteOf: %q %q", siteOf("API.OpenAI.com."), siteOf("x.github.io"))
	}
}

func TestForwardAdmitsOnlyCallsThatHoldASlot(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var hits atomic.Int32
	p := slowProxy(t, Options{InFlight: 1}, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/slow" {
			started <- struct{}{}
			<-release
		}
	})
	var admitted atomic.Int32
	admit := func() *apierr.Error { admitted.Add(1); return nil }
	slow := route(t, `{"upstream":"https://example.com/slow","methods":["GET"]}`)
	rules := route(t, `{"upstream":"https://example.com/fast","methods":["POST"],"body":{"pin":{"model":"m"}}}`)
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		p.Forward(rec, httptest.NewRequest("GET", "/x", nil), Call{Route: slow, Admit: admit})
		done <- rec.Code
	}()
	<-started
	rec := httptest.NewRecorder()
	p.Forward(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)), Call{Route: rules, Admit: admit})
	if rec.Code != http.StatusServiceUnavailable || admitted.Load() != 1 {
		t.Fatalf("busy call: %d, admitted %d", rec.Code, admitted.Load())
	}
	close(release)
	<-done
	rec = httptest.NewRecorder()
	p.Forward(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`not json`)), Call{Route: rules, Admit: admit})
	if rec.Code != 400 || admitted.Load() != 1 {
		t.Fatalf("bad body: %d, admitted %d", rec.Code, admitted.Load())
	}
	refuse := func() *apierr.Error { return apierr.RateLimited(60) }
	before := hits.Load()
	rec = httptest.NewRecorder()
	p.Forward(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)), Call{Route: rules, Admit: refuse})
	if rec.Code != 429 || hits.Load() != before {
		t.Fatalf("refused admit: %d, upstream hit %v", rec.Code, hits.Load() != before)
	}
	rec = httptest.NewRecorder()
	p.Forward(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)), Call{Route: rules, Admit: admit})
	if rec.Code != 200 || admitted.Load() != 2 {
		t.Fatalf("good call: %d, admitted %d", rec.Code, admitted.Load())
	}
}

func TestForwardAppliesBodyRulesToEveryMethod(t *testing.T) {
	var got []byte
	var gotLen int64
	p, _ := upstream(t, func(w http.ResponseWriter, r *http.Request) { got, _ = io.ReadAll(r.Body); gotLen = r.ContentLength })
	rt := route(t, `{"upstream":"https://example.com/x","methods":["POST","DELETE"],"body":{"pin":{"model":"cheap"}}}`)
	if rec := call(p, rt, httptest.NewRequest("DELETE", "/x", strings.NewReader(`{"model":"pricey","id":1}`)), ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if !strings.Contains(string(got), `"model":"cheap"`) {
		t.Fatalf("DELETE body skipped the rules: %s", got)
	}
	req := httptest.NewRequest("DELETE", "/x", strings.NewReader(`not json`))
	if rec := call(p, rt, req, ""); rec.Code != 400 {
		t.Fatalf("DELETE with a bad body: %d", rec.Code)
	}
	got = []byte("unset")
	if rec := call(p, rt, httptest.NewRequest("DELETE", "/x", nil), ""); rec.Code != 200 || len(got) != 0 || gotLen != 0 {
		t.Fatalf("DELETE without a body: %d %q", rec.Code, got)
	}
	// A POST to a route with rules must carry a body for them to apply to.
	if rec := call(p, rt, httptest.NewRequest("POST", "/x", nil), ""); rec.Code != 400 {
		t.Fatalf("POST without a body: %d", rec.Code)
	}
}

func TestBufferedBodyBudget(t *testing.T) {
	p := New(Options{})
	r1, ok := p.reserve(MaxBufferedBytes - 10)
	if !ok {
		t.Fatal("first reservation")
	}
	if _, ok := p.reserve(11); ok {
		t.Fatal("over the budget")
	}
	r1()
	r1()
	if _, ok := p.reserve(MaxBufferedBytes); !ok || p.buffered != MaxBufferedBytes {
		t.Fatalf("release did not return the bytes once: %d", p.buffered)
	}
}
