package serve

import (
	"context"
	"net/http"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/store"
)

// Mount is a custom-domain address an artifact is being served under:
// https://docs.example.com/guide/ is Host docs.example.com, Base /guide/.
//
// Everything the share origin does for a page under /a/{id}/ happens the
// same way under a mount: the access checks, the share-code gate, the
// comment runtime, pinned versions under v/{n}/, the cover at _cover.svg
// and the unlock form at _unlock. Code that builds a page's own URLs or
// scopes a cookie to it must ask PageBase rather than writing /a/{id}/.
type Mount struct {
	Scheme, Host, Base, ArtifactID string
	// TenantID owns the domain. A mounted artifact that has since moved to
	// another workspace is not served there.
	TenantID string
}

type mountKey struct{}

func mountFrom(r *http.Request) (Mount, bool) {
	m, ok := r.Context().Value(mountKey{}).(Mount)
	return m, ok
}

// OnMount reports whether the request is being served on a custom domain.
func OnMount(r *http.Request) bool {
	_, ok := mountFrom(r)
	return ok
}

// foreignToMount reports an artifact the custom domain may not serve: one
// that belongs to a workspace other than the domain's.
func foreignToMount(r *http.Request, a store.Artifact) bool {
	m, ok := mountFrom(r)
	return ok && (a.ID != m.ArtifactID || a.TenantID != m.TenantID)
}

// mountOrigin is the custom domain's origin, scheme://host.
func (m Mount) origin() string { return m.Scheme + "://" + m.Host }

// ServeMount answers a request on a custom domain. rest is the request path
// below m.Base, the same thing {path...} holds under /a/{id}/.
func (h *Handler) ServeMount(w http.ResponseWriter, r *http.Request, m Mount, rest string) {
	r = r.WithContext(context.WithValue(r.Context(), mountKey{}, m))
	r.SetPathValue("id", m.ArtifactID)
	// A custom domain is same-site with whatever else its owner runs on the
	// parent domain, so its writes need its own Origin, as on a page host.
	if CrossPageWrite(r) {
		h.refuseCrossPage(w, r)
		return
	}
	if rest == proxyPrefix || strings.HasPrefix(rest, proxyPrefix+"/") {
		w.Header().Set("Cache-Control", "no-store")
		if !SameOriginRequest(r) {
			h.refuseCrossPage(w, r)
			return
		}
		h.ServeProxy(w, r, m.ArtifactID, "/"+strings.TrimPrefix(strings.TrimPrefix(rest, proxyPrefix), "/"))
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
	// v/{version}/{path...}, exactly as the share origin's pattern reads it.
	if after, ok := strings.CutPrefix(rest, "v/"); ok {
		if version, p, ok := strings.Cut(after, "/"); ok && version != "" {
			r.SetPathValue("version", version)
			r.SetPathValue("path", p)
			h.pinned(w, r)
			return
		}
	}
	r.SetPathValue("path", rest)
	h.current(w, r)
}
