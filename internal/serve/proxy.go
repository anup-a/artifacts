package serve

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/guest"
	"github.com/anup-a/artifacts/internal/proxy"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
)

// Routes runs pages' proxy routes: /a/{id}/_proxy/{route}[/{sub...}]. The
// path lives under the page's own address, so the page calls it with a
// relative URL from its own origin, and the unlock cookie of a code-shared
// page (scoped to /a/{id}/) comes along.
type Routes struct {
	Proxy *proxy.Proxy
	// Secrets opens the named variables of a workspace. Names it does not
	// have are left out of the map.
	Secrets func(ctx context.Context, tenantID string, names []string) (map[string]proxy.Secret, error)
	// PerIP and PerPage meter calls per client address (across pages) and
	// per page (across addresses).
	PerIP, PerPage *ratelimit.Limiter
	ClientIP       func(*http.Request) string
	// Reserved are host names routes may not call: this server's origins.
	Reserved []string

	mu     sync.Mutex
	parsed map[string]proxy.Manifest // by the manifest blob's sha256
}

const manifestCacheSize = 512

func (h *Handler) registerRoutes(mux *http.ServeMux) {
	if h.Routes == nil {
		return
	}
	for _, m := range proxy.Methods {
		mux.HandleFunc(m+" /a/{id}/"+proxy.PathPrefix+"/{rest...}", h.publicArtifact(h.proxyCall))
	}
}

func (h *Handler) proxyCall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	esc := strings.TrimPrefix(r.URL.EscapedPath(), "/a/"+url.PathEscape(r.PathValue("id"))+"/"+proxy.PathPrefix)
	h.ServeProxy(w, r, r.PathValue("id"), esc)
}

// ServeProxy answers one proxy route call for artifact id. rest is the
// escaped path after "_proxy": "/{route}" or "/{route}/{sub...}". A custom
// domain serving a page at its root routes "/_proxy/..." here too.
func (h *Handler) ServeProxy(w http.ResponseWriter, r *http.Request, id, rest string) {
	rt := h.Routes
	if rt == nil {
		apierr.Write(w, r, apierr.NotFound("proxy route"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// A request this server sent upstream has come back: a route pointed at
	// byagent through some redirector. Stop it here rather than loop.
	if r.Header.Get(proxy.LoopHeader) != "" {
		apierr.Write(w, r, apierr.Forbidden("proxy routes may not call byagent"))
		return
	}
	if e := sameOrigin(r); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if ok, retry := rt.PerIP.Allow("ip:" + rt.ClientIP(r)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	a, err := h.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apierr.Write(w, r, apierr.NotFound("proxy route"))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	// The same gate as the page: a private page answers only a browser that
	// has unlocked it with its share code, and nothing reveals it otherwise.
	if !h.canView(r, a) {
		apierr.Write(w, r, apierr.NotFound("proxy route"))
		return
	}
	// A password or restricted page needs its own grant (the access
	// cookie, still matching the current password and allowlist) whatever
	// its visibility says, exactly as the page and its comments do.
	if (a.AccessMode == AccessPassword || a.AccessMode == AccessRestricted) && !h.unlocked(r, a) {
		apierr.Write(w, r, apierr.NotFound("proxy route"))
		return
	}
	if a.DeletedAt != nil {
		apierr.Write(w, r, apierr.Gone())
		return
	}
	t, err := h.Store.Tenant(r.Context(), a.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if t.Plan == guest.Plan {
		apierr.Write(w, r, apierr.NotFound("proxy route"))
		return
	}
	m, e := h.liveManifest(r.Context(), a)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	name, sub, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	if sub != "" {
		sub = "/" + sub
	}
	route, ok := m.Routes[name]
	if !ok {
		apierr.Write(w, r, apierr.NotFound("proxy route"))
		return
	}
	sub, ok = proxy.CleanSub(sub)
	if !ok || !route.MatchPath(sub) {
		apierr.Write(w, r, apierr.NotFound("proxy route path"))
		return
	}
	if !route.Allows(r.Method) {
		w.Header().Set("Allow", strings.Join(route.Methods, ", "))
		apierr.Write(w, r, apierr.MethodNotAllowed("route "+name+" accepts "+strings.Join(route.Methods, ", ")))
		return
	}
	// Only a call that reaches a real route spends the page's budget, so
	// probing missing routes cannot use it up. The owner's own cap in the
	// manifest applies when it is lower than the server's.
	if ok, retry := rt.PerPage.AllowMax("page:"+a.ID, m.LimitPerHour); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	label := a.ID + "/" + name
	have := map[string]proxy.Secret{}
	if names := route.Vars(); len(names) > 0 {
		if have, err = rt.Secrets(r.Context(), a.TenantID, names); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	}
	vals, err := route.Resolve(have)
	if err != nil {
		// The detail (which variable, which host) is for the owner's logs; a
		// visitor learns only that the route does not work.
		slog.Warn("proxy route unavailable", "call", label, "reason", err.Error())
		apierr.Write(w, r, apierr.BadGateway("route unavailable"))
		return
	}
	rt.Proxy.Forward(w, r, proxy.Call{Route: route, Sub: sub, Secrets: vals, Label: label, Tenant: a.TenantID})
}

// sameOrigin refuses calls a browser makes from another site. Without it any
// web page could make its visitors' browsers spend this page's routes. A
// client that is not a browser sends neither header and is held by the rate
// limits instead.
func sameOrigin(r *http.Request) *apierr.Error {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return apierr.Forbidden("proxy routes answer only their own page")
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return apierr.Forbidden("proxy routes answer only their own page")
		}
	}
	return nil
}

// liveManifest reads .byagent/proxy.json from the artifact's current version,
// never a pinned or older one, so withdrawing a route takes effect on the
// next publish. Parsed manifests are cached by content hash.
func (h *Handler) liveManifest(ctx context.Context, a store.Artifact) (proxy.Manifest, *apierr.Error) {
	// Only a version whose manifest passed the publish checks runs routes:
	// one published by a guest key, for an end user without the opt-in, or
	// before routes existed has a .byagent/proxy.json that nobody vetted,
	// and restoring such a version must not switch it on.
	vetted, err := h.Store.VersionProxyRoutes(ctx, a.ID, a.CurrentVersion)
	if err != nil {
		return proxy.Manifest{}, apierr.Internal(err)
	}
	if !vetted {
		return proxy.Manifest{}, apierr.NotFound("proxy route")
	}
	files, err := h.Store.Files(ctx, a.ID, a.CurrentVersion)
	if err != nil {
		return proxy.Manifest{}, apierr.Internal(err)
	}
	var f *store.File
	for i := range files {
		if files[i].Path == proxy.ManifestPath {
			f = &files[i]
		}
	}
	if f == nil {
		return proxy.Manifest{}, apierr.NotFound("proxy route")
	}
	rt := h.Routes
	rt.mu.Lock()
	m, ok := rt.parsed[f.SHA256]
	rt.mu.Unlock()
	if ok {
		return m, nil
	}
	rc, _, err := h.Blobs.Get(ctx, f.SHA256)
	if err != nil {
		return proxy.Manifest{}, apierr.Internal(err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, proxy.MaxManifestBytes+1))
	if err != nil {
		return proxy.Manifest{}, apierr.Internal(err)
	}
	m, err = proxy.Parse(raw, rt.Reserved...)
	if err != nil {
		// Publish refuses an invalid manifest, so this is one published under
		// older rules. Its routes stay off rather than half working.
		return proxy.Manifest{}, apierr.NotFound("proxy route")
	}
	rt.mu.Lock()
	if rt.parsed == nil || len(rt.parsed) >= manifestCacheSize {
		rt.parsed = map[string]proxy.Manifest{}
	}
	rt.parsed[f.SHA256] = m
	rt.mu.Unlock()
	return m, nil
}

// hiddenFile reports whether a published path configures the page rather
// than belongs to it. Those files are never served.
func hiddenFile(p string) bool { return strings.HasPrefix(p, proxy.HiddenDir) }
