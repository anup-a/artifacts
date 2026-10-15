package serve

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/pageurl"
	"github.com/anup-a/artifacts/internal/proxy"
	"github.com/anup-a/artifacts/internal/store"
)

// PageHost is a request being answered on a page's own host,
// https://{slug}.{CONTENT_DOMAIN}/. The host serves that page and nothing
// else: its content at /, pinned versions at /v/{n}/, the cover at
// /_cover.svg, the gate's form at /_unlock, the page's state at /_state,
// the comment runtime at /_rt/comments.js and the page's comment routes at
// /c/{its id}/.
//
// That is what makes a page an origin of its own. Its script can read and
// write only its own host, and the cookies a visitor holds for it (an
// unlock, later a password or email grant) are host-only, so no other page
// can read them or have the browser attach them to a request it forges
// without that page's Origin on it (see CrossPageWrite).
//
// Code that builds a page's own URLs or scopes a cookie to it asks PageBase
// and the cookie helpers in cookie.go rather than writing /a/{id}/.
type PageHost struct {
	// Origin is scheme://{slug}.{domain}, what an Origin header from the
	// page itself carries.
	Origin     string
	ArtifactID string
}

type pageHostKey struct{}

// WithPageHost marks r as served on p's host. The host dispatch in package
// api calls it once it has resolved the Host header to a page, before any
// handler (these or the comment routes) sees the request.
func WithPageHost(r *http.Request, p PageHost) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), pageHostKey{}, p))
}

func pageHostFrom(r *http.Request) (PageHost, bool) {
	p, ok := r.Context().Value(pageHostKey{}).(PageHost)
	return p, ok
}

// PageHostOf returns the page host r is being answered on, if any.
func PageHostOf(r *http.Request) (PageHost, bool) { return pageHostFrom(r) }

// OnPageHost reports whether r is being answered on a page's own host.
func OnPageHost(r *http.Request) bool {
	_, ok := pageHostFrom(r)
	return ok
}

// PageBase is the path a page's own addresses hang off for this request:
// "/" on its own host, the mount's base on a custom domain, /a/{id}/ on the
// share origin.
func PageBase(r *http.Request, id string) string {
	if p, ok := pageHostFrom(r); ok && p.ArtifactID == id {
		return "/"
	}
	if m, ok := mountFrom(r); ok && m.ArtifactID == id {
		return m.Base
	}
	return "/a/" + id + "/"
}

// pageOrigin is the absolute origin of the page being served, for addresses
// that leave the page: og:image and og:url, fetched by link unfurlers.
func (h *Handler) pageOrigin(r *http.Request) string {
	if o := ownOrigin(r); o != "" {
		return o
	}
	return h.ShareOrigin
}

// ownOrigin is the origin a page answers on when it is not the share
// origin: its page host, or the custom domain it is mounted on. "" else.
func ownOrigin(r *http.Request) string {
	if p, ok := pageHostFrom(r); ok {
		return p.Origin
	}
	if m, ok := mountFrom(r); ok {
		return m.origin()
	}
	return ""
}

// CrossPageWrite reports a state-changing request on a page host that the
// page itself did not make.
//
// Until the content domain is on the Public Suffix List, every page host is
// same-site with every other, so SameSite=Lax does not stop
// a.byagent.page from posting to b.byagent.page with b's cookies attached.
// The Origin header does: it must be the page's own origin. A request with
// no usable Origin (absent, or "null", which browsers send for a form on a
// no-referrer page such as the gate) passes only on Sec-Fetch-Site:
// same-origin. Anything else, including a client that sends neither header,
// is refused: page hosts are for browsers, and the API is on its own origin.
//
// Safe methods are never refused here; nothing on a page host changes state
// on a GET (the one exception, a share code on the link, is metered per page
// and grants nothing the code itself does not).
func CrossPageWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return !SameOriginRequest(r)
}

// SameOriginRequest reports whether r came from the page host's own page
// (or, on a custom domain, from that domain), by the rule CrossPageWrite
// describes. Off a page host or custom domain it is always false.
func SameOriginRequest(r *http.Request) bool {
	return SameOrigin(r, ownOrigin(r))
}

// CrossOriginWrite reports a state-changing request that did not come from
// origin, by the same rule: the check custom domains get at the host
// dispatch.
func CrossOriginWrite(r *http.Request, origin string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return !SameOrigin(r, origin)
}

// SameOrigin is that rule for a given origin: Origin equal to it, or, when
// Origin is absent or "null", Sec-Fetch-Site: same-origin.
func SameOrigin(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return strings.EqualFold(o, origin)
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// ServeRuntime serves the comment runtime, for a page host's
// /_rt/comments.js.
func (h *Handler) ServeRuntime(w http.ResponseWriter, r *http.Request) { h.runtime(w, r) }

// pageRoutes are the endpoints a page host answers besides the page's own
// files, keyed "METHOD path-below-the-page". They are what the share origin
// registers under /a/{id}/ (see Register); a write among them runs only
// after CrossPageWrite has passed. Page access control's gate forms are
// among them; their own crossSite check still runs after it.
func (h *Handler) pageRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		http.MethodPost + " " + unlockPath:      h.submitCode,
		http.MethodPost + " " + accessEmailPath: h.requestAccessCode,
		http.MethodPost + " " + accessCodePath:  h.submitAccessCode,
		http.MethodGet + " " + accessLinkPath:   h.openAccessLink,
		http.MethodPost + " " + accessLinkPath:  h.confirmAccessLink,
		http.MethodGet + " " + statePath:        h.readState,
		http.MethodPost + " " + statePath:       h.writeState,
	}
}

// pageURLs is the page-address builder, with the share origin filled in
// when the handler was built without one (tests).
func (h *Handler) pageURLs() pageurl.Builder {
	b := h.Pages
	if b.ShareOrigin == "" {
		b.ShareOrigin = strings.TrimRight(h.ShareOrigin, "/")
	}
	return b
}

// proxyPrefix is where a page's proxy routes live on its host,
// /_proxy/{route}, so no page file shadows them.
const proxyPrefix = proxy.PathPrefix

// ServePage answers a request on a page's own host (r carries WithPageHost).
// The request path is the page's own: "/" is its index, the same thing
// /a/{id}/ is on the share origin.
func (h *Handler) ServePage(w http.ResponseWriter, r *http.Request) {
	p, ok := pageHostFrom(r)
	if !ok {
		apierr.Write(w, r, apierr.NotFound("artifact"))
		return
	}
	r.SetPathValue("id", p.ArtifactID)
	rest := strings.TrimPrefix(r.URL.Path, "/")
	if CrossPageWrite(r) {
		h.refuseCrossPage(w, r)
		return
	}
	if rest == proxyPrefix || strings.HasPrefix(rest, proxyPrefix+"/") {
		h.pageProxy(w, r)
		return
	}
	if route, ok := h.pageRoutes()[r.Method+" "+rest]; ok {
		h.securityHeaders(w, r)
		route(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		apierr.Write(w, r, &apierr.Error{Code: "METHOD_NOT_ALLOWED", Status: http.StatusMethodNotAllowed, Message: "method not allowed"})
		return
	}
	if rest == coverPath {
		h.cover(w, r)
		return
	}
	// v/{version}/{path...}, read exactly as the share origin's pattern does.
	if after, ok := strings.CutPrefix(rest, "v/"); ok {
		if version, file, ok := strings.Cut(after, "/"); ok && version != "" {
			r.SetPathValue("version", version)
			r.SetPathValue("path", file)
			h.pinned(w, r)
			return
		}
	}
	r.SetPathValue("path", rest)
	h.current(w, r)
}

// refuseCrossPage answers a write that another page (or another site)
// tried to make on this page's behalf.
func (h *Handler) refuseCrossPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	apierr.Write(w, r, apierr.Forbidden("requests to a page must come from the page itself"))
}

// pageProxy answers a page's proxy routes (#72) on its own host:
// /_proxy/{route}[/{sub...}], belonging to this page only. The same-origin
// rule applies to every method, reads included, because a proxied GET still
// spends the page's quota and secrets; ServeProxy then runs its own checks
// (grant, rate limits, manifest) exactly as under /a/{id}/_proxy/.
func (h *Handler) pageProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !SameOriginRequest(r) {
		h.refuseCrossPage(w, r)
		return
	}
	p, _ := pageHostFrom(r)
	h.ServeProxy(w, r, p.ArtifactID, strings.TrimPrefix(r.URL.EscapedPath(), "/"+proxyPrefix))
}

// redirectToPageHost moves a share-origin address of a page, /a/{id}/...,
// to the page's own host with the path below the page and the query kept,
// with a 308 so a form post is replayed as a post. It answers whether it
// wrote a response.
//
// Every request is moved, frames and images included: once pages have hosts
// of their own, no page content may run on the share origin, or a page
// could still reach whatever that origin's cookies open. A share link
// (?k=) arrives on the new host with its code and unlocks there.
//
// A private page that nobody outside the workspace can open is not
// redirected: the redirect would name its host to someone who has only the
// id, and its signed-in readers use /app/view/{id}/ anyway.
func (h *Handler) redirectToPageHost(w http.ResponseWriter, r *http.Request) bool {
	if !h.Pages.PerPage() {
		return false
	}
	id := r.PathValue("id")
	a, err := h.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		h.refuse(w, r)
		return true
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return true
	}
	if a.DeletedAt != nil {
		w.Header().Set("Cache-Control", "no-cache")
		apierr.Write(w, r, apierr.Gone())
		return true
	}
	if a.Slug == "" {
		// A row the slug backfill has not reached cannot have a host yet;
		// keep serving it here rather than lose it.
		return false
	}
	if a.Visibility == "private" && !hasDoor(a) {
		h.refuse(w, r)
		return true
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.EscapedPath(), "/a/"+id), "/")
	target := h.Pages.Origin(a.Slug) + "/" + rest
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
		// A share code rides in the query; keep it out of shared caches.
		w.Header().Set("Cache-Control", "no-store")
	} else {
		// Permanent redirects are cached without end unless told otherwise;
		// an hour lets a rollback (CONTENT_DOMAIN unset) be believed.
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.Header().Set("Location", target)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusPermanentRedirect)
	return true
}
