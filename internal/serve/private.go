package serve

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/store"
)

type privateViewerKey struct{}

func isPrivateViewer(r *http.Request) bool {
	_, ok := r.Context().Value(privateViewerKey{}).(string)
	return ok
}

func (h *Handler) canView(r *http.Request, a store.Artifact) bool {
	if tenant, ok := r.Context().Value(privateViewerKey{}).(string); ok {
		return tenant == a.TenantID
	}
	return a.Visibility != "private"
}

// separateOrigins reports whether the dashboard and the share host are
// distinct, which is what private viewing needs to isolate uploaded pages.
func (h *Handler) separateOrigins() bool {
	return !strings.EqualFold(originHost(h.APIOrigin), originHost(h.ShareOrigin))
}

func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Host
}

// Uploaded pages must never execute with the dashboard's origin, including
// public pages reached by manually replacing the host in a share URL. This
// only applies where the two origins are actually separate: a single-origin
// deployment shares one host, serves public artifacts from it as it always
// has, and offers no private viewing at all.
func (h *Handler) publicArtifact(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Sessions != nil && h.separateOrigins() && strings.EqualFold(r.Host, originHost(h.APIOrigin)) {
			w.Header().Set("Cache-Control", "no-store")
			apierr.Write(w, r, apierr.NotFound("artifact"))
			return
		}
		next(w, r)
	}
}

// Private content uses the existing /app session on the app origin. An HTTP
// CSP sandbox gives every uploaded document an opaque origin, even when it
// is opened outside an iframe. Never add allow-same-origin here: uploaded
// JavaScript must not be able to read the dashboard or another artifact.
func (h *Handler) privateArtifact(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !strings.EqualFold(r.Host, originHost(h.APIOrigin)) || !h.separateOrigins() {
			apierr.Write(w, r, apierr.NotFound("artifact"))
			return
		}
		sess, err := h.Sessions.Load(r.Context(), w, r)
		if errors.Is(err, auth.ErrNoSession) {
			h.denyPrivate(w, r)
			return
		}
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), privateViewerKey{}, sess.TenantID))
		next(w, r)
	}
}

// denyPrivate answers a /app/view address the visitor may not read. Deny
// renders the app's access page; without it (an API-only server, or a test
// that mounts serve on its own) the plain 404 stands. Every refusal on that
// path goes through here — no session, wrong workspace, and no such artifact
// alike — so the response cannot be used to tell those apart.
func (h *Handler) denyPrivate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.Deny != nil {
		h.Deny(w, r)
		return
	}
	apierr.Write(w, r, apierr.NotFound("artifact"))
}

// refuse answers a request for something the caller may not have. The share
// origin keeps its bare 404: it serves machines and embeds as much as people,
// and an HTML page there would be a body where a 404 belongs.
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request) {
	if isPrivateViewer(r) {
		h.denyPrivate(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	apierr.Write(w, r, apierr.NotFound("artifact"))
}

func (h *Handler) artifactCacheHeaders(w http.ResponseWriter, r *http.Request) {
	if isPrivateViewer(r) {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		// A public artifact may become private. Revalidate every read, including
		// pinned versions and covers, so no shared cache can bypass authorization.
		w.Header().Set("Cache-Control", "no-cache")
	}
}
