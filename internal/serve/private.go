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
	if a.Visibility != "private" {
		return true
	}
	// A private artifact with a share code is readable by anyone who has
	// already presented it. The code opens this artifact and no other: the
	// cookie is scoped to its path and signed over its id.
	return h.unlocked(r, a)
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

// refuseOrGate is the refusal for a page request on the share origin. An
// artifact shared by code asks for the code instead of denying outright:
// its owner deliberately handed the address around, so "this needs a code"
// is the honest answer rather than pretending nothing is there.
//
// Only whole-page requests reach the gate. An asset or a cover answers a
// plain 404, because a viewer never arrives at one of those first and a gate
// page delivered where a stylesheet was expected is only confusing.
func (h *Handler) refuseOrGate(w http.ResponseWriter, r *http.Request, a store.Artifact, page bool) {
	if page && !isPrivateViewer(r) && a.ShareCode != "" && h.Gate != nil {
		// A cookie that no longer matches means the code was rotated; drop it
		// so the viewer is asked again rather than silently refused forever.
		clearUnlockCookie(w, a.ID)
		h.renderGate(w, r, GateView{ArtifactID: a.ID})
		return
	}
	h.refuse(w, r)
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
