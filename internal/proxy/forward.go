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
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
)

// Limits on one proxied call.
const (
	MaxRequestBytes  = 4 << 20
	MaxResponseBytes = 10 << 20
	// Timeout bounds the whole exchange, streamed body included: long enough
	// for a streamed model answer, short enough that a stuck upstream frees
	// its connection.
	Timeout        = 120 * time.Second
	headerTimeout  = 60 * time.Second
	maxHeaderBytes = 64 << 10
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
}

// Proxy forwards route calls. It is safe for concurrent use.
type Proxy struct {
	client  *http.Client
	timeout time.Duration
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
	return &Proxy{timeout: timeout, client: &http.Client{
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
}

// Forward sends the visitor's request upstream and streams the answer back.
// Secrets appear only in the upstream request; errors and logs never carry
// them, and an upstream that echoes one has it replaced in the response.
func (p *Proxy) Forward(w http.ResponseWriter, r *http.Request, c Call) {
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()
	var body io.Reader
	if r.Body != nil && r.Method != http.MethodGet {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
		if err != nil {
			apierr.Write(w, r, apierr.TooLarge("request body is larger than 4 MB"))
			return
		}
		body = bytes.NewReader(raw)
	}
	target := c.target(r.URL.Query())
	out, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(errors.New("proxy: building the upstream request failed")))
		return
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

func (p *Proxy) upstreamError(w http.ResponseWriter, r *http.Request, c Call, err error) {
	slog.Warn("proxy upstream failed", "call", c.Label, "host", c.Route.Upstream.Host, "err", safeErr(err))
	var ne net.Error
	switch {
	case errors.Is(err, errBlocked):
		apierr.Write(w, r, apierr.Forbidden("the upstream host resolves to an address proxy routes may not reach"))
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
				dst.Flush()
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
			dst.Flush()
			return n, err
		}
	}
}
