package telemetry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/pageurl"
)

// Host kinds, the values of byagent.host_kind. They say which part of
// byagent a request was for without saying which page or domain.
const (
	// HostSite is the public site (byagent.dev, and its www redirect).
	HostSite = "site"
	// HostApp is the dashboard and API origin (app.byagent.dev).
	HostApp = "app"
	// HostPage is a page's own host ({slug}.CONTENT_DOMAIN), whether or
	// not a page has that slug.
	HostPage = "page"
	// HostCustomDomain is a custom domain connected to a workspace. Only
	// the router can tell one from a stray name, so it sets this itself
	// (SetHostKind).
	HostCustomDomain = "custom_domain"
	// HostOther is everything else: a health check by address, the bare
	// content domain, a name nobody connected.
	HostOther = "other"
)

// routeUnmatched is http.route for a request no ServeMux pattern answered:
// a redirect to the apex, a refusal before routing, a 405.
const routeUnmatched = "unmatched"

// Attribute keys this package adds beside the semantic conventions.
const (
	hostKindKey      = attribute.Key("byagent.host_kind")
	publishKindKey   = attribute.Key("byagent.publish.kind")   // guest, key
	publishResultKey = attribute.Key("byagent.publish.result") // ok, error
)

// durationBuckets suit a small web app: most answers take a few
// milliseconds, a publish upload or a slow proxy call can take seconds.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// sizeBuckets go from a JSON error to a large page asset, in powers of four.
var sizeBuckets = []float64{0, 256, 1 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10, 1 << 20, 4 << 20, 16 << 20}

// labels is what the inner handlers learn about a request that the
// middleware, outside them, cannot see for itself. One is put in the
// request context per request and filled in as the request is routed.
//
// It exists because the request the middleware holds is not the one the
// muxes route: requestID and the host router each pass a copy made with
// r.WithContext down the chain, and ServeMux records the matched pattern
// (r.Pattern) on the copy it was given. A pointer in the context is shared
// by every copy.
type labels struct {
	route    string
	hostKind string
	// publish is "guest" or "key" when the request is a publish, so its
	// outcome can be counted from the status it ends with.
	publish string
}

type labelsKey struct{}

func labelsFrom(ctx context.Context) *labels {
	l, _ := ctx.Value(labelsKey{}).(*labels)
	return l
}

// Routed wraps a ServeMux so the pattern it matched becomes the request's
// http.route. Wrap every mux a request can be routed by; without
// telemetry on it costs one context lookup.
//
// The first mux to record a pattern wins, so with muxes nested the
// innermost, most specific, one does: it returns first.
func Routed(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := labelsFrom(r.Context())
		if l == nil {
			mux.ServeHTTP(w, r)
			return
		}
		// ServeMux sets r.Pattern on this very request before it calls the
		// handler, so it is there to read afterwards, even after a panic.
		defer func() {
			if l.route == "" && r.Pattern != "" {
				l.route = routeOf(r.Pattern)
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

// SetHostKind overrides the host kind the middleware worked out from the
// Host header, for what only the router knows (HostCustomDomain).
func SetHostKind(ctx context.Context, kind string) {
	if l := labelsFrom(ctx); l != nil {
		l.hostKind = kind
	}
}

// MarkPublish marks the request as a publish by a guest or a real key. The
// middleware counts it in byagent.publishes once the response is written,
// as ok below status 400 and error otherwise, so every early return in the
// handler is counted without touching it.
func MarkPublish(ctx context.Context, guest bool) {
	if l := labelsFrom(ctx); l != nil {
		l.publish = "key"
		if guest {
			l.publish = "guest"
		}
	}
}

// routeOf turns a ServeMux pattern ("GET /api/artifacts/{id}") into an
// http.route: the path template alone, without the method or a host.
func routeOf(pattern string) string {
	if i := strings.IndexAny(pattern, " \t"); i >= 0 {
		pattern = strings.TrimLeft(pattern[i:], " \t")
	}
	if i := strings.IndexByte(pattern, '/'); i > 0 {
		pattern = pattern[i:]
	}
	if pattern == "" {
		return routeUnmatched
	}
	return pattern
}

// Hosts sorts a request's Host into a host kind, the way the router does,
// without a database lookup.
type Hosts struct {
	// Site and App are the share and API origins' host names.
	Site, App string
	Pages     pageurl.Builder
}

// HostsFor reads the host names from the server config.
func HostsFor(cfg config.Config) Hosts {
	return Hosts{Site: originHost(cfg.ShareOrigin), App: originHost(cfg.APIOrigin), Pages: pageurl.New(cfg)}
}

func originHost(origin string) string {
	_, rest, ok := strings.Cut(origin, "://")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, "/")
	return pageurl.Hostname(rest)
}

// Kind classifies a Host header. Page hosts are checked first, as the
// router checks them first: with a shared content domain a slug-shaped
// name under byagent.dev is a page even though byagent.dev is the site.
func (h Hosts) Kind(host string) string {
	if k, _ := h.Pages.Classify(host); k == pageurl.Page {
		return HostPage
	}
	host = pageurl.Hostname(host)
	switch {
	case host == "":
		return HostOther
	case host == h.Site || host == "www."+h.Site:
		return HostSite
	case host == h.App:
		return HostApp
	}
	return HostOther
}

// Middleware records the HTTP server metrics for every request through
// next. It must be the outermost handler, so the duration covers all of the
// server's work and the labels are in the context before anything copies
// the request.
//
// Every attribute comes from a short, fixed set: the method (unknown ones
// become _OTHER), the status code, the matched route pattern and the host
// kind. No path, host, slug, id, address or user agent is ever recorded.
func Middleware(mp metric.MeterProvider, hosts Hosts, next http.Handler) (http.Handler, error) {
	m := mp.Meter(scope)
	duration, err := m.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(durationBuckets...))
	if err != nil {
		return nil, err
	}
	size, err := m.Int64Histogram("http.server.response.body.size",
		metric.WithUnit("By"),
		metric.WithDescription("Size of HTTP server response bodies."),
		metric.WithExplicitBucketBoundaries(sizeBuckets...))
	if err != nil {
		return nil, err
	}
	active, err := m.Int64UpDownCounter("http.server.active_requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("Number of active HTTP server requests."))
	if err != nil {
		return nil, err
	}
	publishes, err := m.Int64Counter("byagent.publishes",
		metric.WithUnit("{publish}"),
		metric.WithDescription("Publishes of a new page or a new version, by key kind and outcome."))
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		method := semconv.HTTPRequestMethodKey.String(methodOf(r.Method))
		// active_requests carries the method alone: the up and the down
		// must name the same series, and the rest is known only at the end.
		activeAttrs := metric.WithAttributes(method)
		active.Add(r.Context(), 1, activeAttrs)

		l := &labels{hostKind: hosts.Kind(r.Host)}
		ctx := context.WithValue(r.Context(), labelsKey{}, l)
		rw := &responseWriter{ResponseWriter: w}
		finished := false
		defer func() {
			// A handler that panicked never wrote its status; net/http
			// answers it by dropping the connection, so count it as a 500.
			status := rw.status
			if status == 0 {
				status = http.StatusOK
				if !finished {
					status = http.StatusInternalServerError
				}
			}
			route := l.route
			if route == "" {
				route = routeUnmatched
			}
			attrs := metric.WithAttributes(method,
				semconv.HTTPResponseStatusCode(status),
				semconv.HTTPRoute(route),
				hostKindKey.String(l.hostKind))
			// A context that outlives the request: a cancelled one is still
			// fine for recording, but there is no reason to depend on that.
			rec := context.WithoutCancel(ctx)
			duration.Record(rec, time.Since(start).Seconds(), attrs)
			size.Record(rec, rw.bytes, attrs)
			active.Add(rec, -1, activeAttrs)
			if l.publish != "" {
				result := "ok"
				if status >= 400 {
					result = "error"
				}
				publishes.Add(rec, 1, metric.WithAttributes(publishKindKey.String(l.publish), publishResultKey.String(result)))
			}
		}()
		next.ServeHTTP(rw, r.WithContext(ctx))
		finished = true
	}), nil
}

// methodOf keeps the methods the semantic conventions name and folds every
// other one, which a client can make up freely, into _OTHER.
func methodOf(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return m
	}
	return "_OTHER"
}

// responseWriter notes the status and counts the body bytes on their way
// out.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseWriter) WriteHeader(code int) {
	// 1xx answers are interim; the final status comes after them.
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// ReadFrom keeps the server's sendfile path for file bodies (io.Copy looks
// for it), which wrapping the writer would otherwise hide.
func (w *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	var n int64
	var err error
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(src)
	} else {
		n, err = io.Copy(w.ResponseWriter, src)
	}
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the real writer, for Flush,
// deadlines and the rest.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush passes through for handlers that assert http.Flusher directly.
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		f.Flush()
	}
}
