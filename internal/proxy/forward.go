package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anup-a/byagent/internal/apierr"
	"golang.org/x/net/publicsuffix"
)

// Limits on one proxied call.
const (
	MaxRequestBytes  = 4 << 20
	MaxResponseBytes = 10 << 20
	// MaxRuleBodyBytes bounds a body the proxy has to read whole, to apply
	// a route's body rules. Other bodies stream straight through.
	MaxRuleBodyBytes = 1 << 20
	// Timeout bounds the whole exchange, streamed body included: long enough
	// for a streamed model answer, short enough that a stuck upstream frees
	// its slot. An upstream must start answering within headerTimeout.
	Timeout        = 60 * time.Second
	headerTimeout  = 20 * time.Second
	maxHeaderBytes = 64 << 10
	// BodyTimeout bounds reading the visitor's request body. The body is
	// read before the call takes a slot, so a client that trickles it in
	// holds nothing but its own connection, and only for this long.
	BodyTimeout = 10 * time.Second
	// MaxBufferedBytes bounds the request bodies held in memory at once,
	// counted at their declared length or, when unknown, at the cap. Past it
	// a call gets 503.
	MaxBufferedBytes = 128 << 20
	// MaxInFlight, MaxInFlightPerTenant and MaxInFlightPerHost cap
	// concurrent calls server-wide, per workspace and per upstream site (its
	// registrable domain), so slow upstreams cannot pin every connection.
	MaxInFlight          = 128
	MaxInFlightPerTenant = 16
	MaxInFlightPerHost   = 32
)

// LoopHeader marks requests this server sends upstream. One arriving here is
// a route pointed (directly or through a redirector) back at byagent.
const LoopHeader = "X-Byagent-Proxy"

// Options adjust where the proxy may connect. The zero value is production:
// real DNS, public addresses only, port 443, system roots. Tests set fields
// to reach a local TLS server.
type Options struct {
	Resolve   func(ctx context.Context, host string) ([]netip.Addr, error)
	AllowAddr func(netip.Addr) bool
	DialPort  string
	RootCAs   *x509.CertPool
	// Timeout replaces the whole-call Timeout, so tests need not wait it out.
	Timeout time.Duration
	// BodyTimeout replaces the request body deadline.
	BodyTimeout time.Duration
	// InFlight, InFlightPerTenant and InFlightPerHost replace the
	// concurrency caps.
	InFlight, InFlightPerTenant, InFlightPerHost int
}

// Proxy forwards route calls. It is safe for concurrent use.
type Proxy struct {
	client      *http.Client
	timeout     time.Duration
	bodyTimeout time.Duration

	mu        sync.Mutex
	inFlight  int
	perTenant map[string]int
	perHost   map[string]int
	maxAll    int
	maxTenant int
	maxHost   int
	buffered  int64
}

// acquire takes a slot for one call, or reports that the server, the
// workspace or the upstream site is at its cap. It never waits: a caller
// over the cap is told to come back, not queued.
func (p *Proxy) acquire(tenant, host string) (release func(), ok bool) {
	site := siteOf(host)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inFlight >= p.maxAll || p.perTenant[tenant] >= p.maxTenant || p.perHost[site] >= p.maxHost {
		return nil, false
	}
	p.inFlight++
	p.perTenant[tenant]++
	p.perHost[site]++
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.inFlight--
		if p.perTenant[tenant]--; p.perTenant[tenant] <= 0 {
			delete(p.perTenant, tenant)
		}
		if p.perHost[site]--; p.perHost[site] <= 0 {
			delete(p.perHost, site)
		}
	}, true
}

// siteOf is the registrable domain of an upstream host ("api.openai.com"
// is "openai.com"), so one owner's many subdomains share one cap.
func siteOf(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// reserve claims n bytes of the buffered-body budget.
func (p *Proxy) reserve(n int64) (release func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buffered+n > MaxBufferedBytes {
		return nil, false
	}
	p.buffered += n
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.buffered -= n
			p.mu.Unlock()
		})
	}, true
}

// New builds the proxy and its HTTP client: no environment proxy, no
// redirects, a dialer that reaches only public addresses on 443.
func New(o Options) *Proxy {
	d := &dialer{resolve: o.Resolve, allow: o.AllowAddr, port: o.DialPort}
	if d.resolve == nil {
		d.resolve = lookup
	}
	if d.allow == nil {
		d.allow = PublicAddr
	}
	if d.port == "" {
		d.port = "443"
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            d.dial,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  headerTimeout,
		MaxResponseHeaderBytes: maxHeaderBytes,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    10,
		IdleConnTimeout:        90 * time.Second,
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = Timeout
	}
	bodyTimeout := o.BodyTimeout
	if bodyTimeout <= 0 {
		bodyTimeout = BodyTimeout
	}
	maxAll, maxTenant, maxHost := o.InFlight, o.InFlightPerTenant, o.InFlightPerHost
	if maxAll <= 0 {
		maxAll = MaxInFlight
	}
	if maxTenant <= 0 {
		maxTenant = MaxInFlightPerTenant
	}
	if maxHost <= 0 {
		maxHost = MaxInFlightPerHost
	}
	return &Proxy{timeout: timeout, bodyTimeout: bodyTimeout, maxAll: maxAll, maxTenant: maxTenant, maxHost: maxHost,
		perTenant: map[string]int{}, perHost: map[string]int{}, client: &http.Client{
			Transport: tr,
			// A redirect could lead anywhere, including somewhere the variable is
			// not pinned to. It is answered as an error, never followed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}
}

// requestHeaders are the only headers a visitor's browser may pass upstream.
// Everything else (cookies, the visitor's own Authorization, Referer,
// Origin, client hints, forwarding headers) stays here.
var requestHeaders = []string{"Accept", "Accept-Language", "Content-Type"}

// responseHeaders are the only upstream headers passed back. Set-Cookie,
// CORS, HSTS, Clear-Site-Data, Location and the rest never reach the page's
// origin.
var responseHeaders = []string{"Content-Type", "Content-Language", "Etag", "Last-Modified", "Retry-After"}

func passResponseHeader(name string) bool {
	c := http.CanonicalHeaderKey(name)
	for _, h := range responseHeaders {
		if c == h {
			return true
		}
	}
	return strings.HasPrefix(c, "X-Ratelimit-") || strings.HasPrefix(c, "Ratelimit-")
}

// Call is one resolved proxy call: the route, the escaped sub-path after the
// route name, and the values of the variables the route refers to.
type Call struct {
	Route   Route
	Sub     string
	Secrets map[string]string
	// Label names the call in logs (artifact and route), never secrets.
	Label string
	// Tenant is the workspace the page belongs to, for the concurrency cap.
	Tenant string
	// Admit, when set, runs once the call holds a slot and just before it
	// goes upstream: the place to spend a budget, so a call refused as busy
	// or for a bad body costs nothing. An error it returns is the answer.
	Admit func() *apierr.Error
}

// Forward sends the visitor's request upstream and streams the answer back.
// Secrets appear only in the upstream request; errors and logs never carry
// them, and an upstream that echoes one has it replaced in the response.
func (p *Proxy) Forward(w http.ResponseWriter, r *http.Request, c Call) {
	// The body comes in first, whole and under a deadline, and only then
	// does the call take a slot: a client that sends it slowly cannot hold
	// slots that other pages' calls need.
	body, unbuffer, e := p.requestBody(w, r, c)
	if e != nil {
		if e.Status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "5")
		}
		apierr.Write(w, r, e)
		return
	}
	defer unbuffer()
	release, ok := p.acquire(c.Tenant, c.Route.Upstream.Hostname())
	if !ok {
		w.Header().Set("Retry-After", "5")
		apierr.Write(w, r, apierr.Unavailable("route busy, try again shortly"))
		return
	}
	defer release()
	if c.Admit != nil {
		if e := c.Admit(); e != nil {
			apierr.Write(w, r, e)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()
	target := c.target(r.URL.Query())
	out, err := http.NewRequestWithContext(ctx, r.Method, target, http.NoBody)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(errors.New("proxy: building the upstream request failed")))
		return
	}
	if body != nil {
		out.Body = io.NopCloser(bytes.NewReader(body))
		out.ContentLength = int64(len(body))
		out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	for _, h := range requestHeaders {
		if v := r.Header.Values(h); len(v) > 0 {
			out.Header[h] = v
		}
	}
	for k, tmpl := range c.Route.Headers {
		out.Header.Set(k, Expand(tmpl, c.Secrets))
	}
	out.Header.Set("User-Agent", "byagent-proxy/1")
	out.Header.Set(LoopHeader, "1")
	start := time.Now()
	resp, err := p.client.Do(out)
	// The body has gone upstream; its bytes no longer count as held.
	unbuffer()
	if err != nil {
		p.upstreamError(w, r, c, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified {
		apierr.Write(w, r, apierr.BadGateway("the upstream answered with a redirect, which proxy routes do not follow; point the route at the final URL"))
		return
	}
	if resp.Header.Get("Content-Encoding") != "" {
		apierr.Write(w, r, apierr.BadGateway("the upstream sent an encoded body the proxy cannot pass on"))
		return
	}
	if resp.ContentLength > MaxResponseBytes {
		apierr.Write(w, r, apierr.BadGateway("the upstream response is larger than 10 MB"))
		return
	}
	red := newRedactor(w, c.Secrets)
	hd := w.Header()
	for k, vs := range resp.Header {
		if !passResponseHeader(k) {
			continue
		}
		for _, v := range vs {
			if !red.contains(v) {
				hd.Add(k, v)
			}
		}
	}
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	// Opened directly in a tab, an upstream HTML or SVG answer must not run
	// as a document on the share origin.
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(resp.StatusCode)
	n, err := copyCapped(red, resp.Body, w)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("proxy response cut", "call", c.Label, "host", c.Route.Upstream.Host, "bytes", n, "err", safeErr(err))
	}
	slog.Info("proxy", "call", c.Label, "host", c.Route.Upstream.Host, "status", resp.StatusCode, "bytes", n, "ms", time.Since(start).Milliseconds())
}

// target builds the upstream URL: the route's upstream, the sub-path below
// it, the visitor's query parameters, then the route's own query values,
// which the visitor can neither replace nor repeat.
func (c Call) target(visitor url.Values) string {
	u := *c.Route.Upstream
	if c.Sub != "" {
		u.RawPath = strings.TrimSuffix(u.EscapedPath(), "/") + c.Sub
		if p, err := url.PathUnescape(u.RawPath); err == nil {
			u.Path = p
		}
	}
	q := u.Query()
	fixed := map[string]bool{}
	for k := range q {
		fixed[k] = true
	}
	for k := range c.Route.Query {
		fixed[k] = true
	}
	for k, vs := range visitor {
		if fixed[k] {
			continue
		}
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	for k, tmpl := range c.Route.Query {
		q.Set(k, Expand(tmpl, c.Secrets))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// requestBody reads what goes upstream, whole, before the call takes a slot:
// nothing for GET; for a route with body rules, the body rewritten by them
// (at most MaxRuleBodyBytes, whatever the method, and required unless the
// method is DELETE); otherwise the visitor's body as sent, at most
// MaxRequestBytes. The read must finish within the body deadline. unbuffer
// returns the bytes to the buffered budget and is safe to call twice.
func (p *Proxy) requestBody(w http.ResponseWriter, r *http.Request, c Call) (body []byte, unbuffer func(), e *apierr.Error) {
	none := func() {}
	if r.Method == http.MethodGet {
		return nil, none, nil
	}
	limit, what := int64(MaxRequestBytes), "4 MB"
	if c.Route.Body != nil {
		limit, what = MaxRuleBodyBytes, "1 MB"
	}
	var raw []byte
	if r.Body != nil && r.Body != http.NoBody {
		if r.ContentLength > limit {
			return nil, none, apierr.TooLarge("request body is larger than " + what)
		}
		hold := limit
		if r.ContentLength >= 0 {
			hold = r.ContentLength
		}
		release, ok := p.reserve(hold)
		if !ok {
			return nil, none, apierr.Unavailable("route busy, try again shortly")
		}
		// Not every ResponseWriter supports deadlines (test recorders do
		// not); the server's own timeouts still apply then.
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(p.bodyTimeout))
		var err error
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			// The deadline stays: the server then gives up on the rest of
			// the body at once instead of waiting to drain it.
			release()
			var mbe *http.MaxBytesError
			var ne net.Error
			switch {
			case errors.As(err, &mbe):
				return nil, none, apierr.TooLarge("request body is larger than " + what)
			case errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
				return nil, none, apierr.RequestTimeout("the request body did not arrive in time")
			default:
				return nil, none, apierr.Invalid("the request body could not be read")
			}
		}
		// Lifted again so the server's watch for the client going away
		// does not take the passed deadline for a closed connection.
		_ = rc.SetReadDeadline(time.Time{})
		unbuffer = release
	} else {
		unbuffer = none
	}
	if c.Route.Body != nil {
		if len(raw) == 0 && r.Method == http.MethodDelete {
			return nil, unbuffer, nil
		}
		out, err := c.Route.Body.ApplyBody(raw)
		if err != nil {
			unbuffer()
			return nil, none, apierr.Invalid(err.Error())
		}
		return out, unbuffer, nil
	}
	if len(raw) == 0 {
		return nil, unbuffer, nil
	}
	return raw, unbuffer, nil
}

func (p *Proxy) upstreamError(w http.ResponseWriter, r *http.Request, c Call, err error) {
	slog.Warn("proxy upstream failed", "call", c.Label, "host", c.Route.Upstream.Host, "err", safeErr(err))
	var ne net.Error
	switch {
	case errors.Is(err, errBlocked):
		// The host is the owner's business; a visitor learns only that the
		// route does not work.
		apierr.Write(w, r, apierr.BadGateway("route unavailable"))
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		apierr.Write(w, r, apierr.GatewayTimeout("the upstream did not answer in time"))
	case errors.Is(err, context.Canceled):
		// The visitor went away; nobody is left to answer.
	default:
		apierr.Write(w, r, apierr.BadGateway("could not reach the upstream"))
	}
}

// safeErr describes a transport error without the request URL, which can
// hold a secret in its query string.
func safeErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// copyCapped streams src to dst, flushing as it goes so server-sent events
// arrive as they are produced, and stops at MaxResponseBytes.
func copyCapped(dst *redactor, src io.Reader, w http.ResponseWriter) (int64, error) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	var n int64
	for {
		k, err := src.Read(buf)
		if k > 0 {
			if n+int64(k) > MaxResponseBytes {
				k = int(MaxResponseBytes - n)
				dst.Write(buf[:k])
				// No Flush: what is held may be the start of a secret
				// whose end was cut off.
				return n + int64(k), errors.New("response larger than the cap")
			}
			if _, werr := dst.Write(buf[:k]); werr != nil {
				return n, werr
			}
			n += int64(k)
			rc.Flush()
		}
		if err == io.EOF {
			dst.Flush()
			rc.Flush()
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}
