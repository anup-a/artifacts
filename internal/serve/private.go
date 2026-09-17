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

func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Host
}

// Uploaded pages must never execute with the dashboard's origin, including
// public pages reached by manually replacing the host in a share URL.
func (h *Handler) publicArtifact(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Sessions != nil && strings.EqualFold(r.Host, originHost(h.APIOrigin)) {
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
		if !strings.EqualFold(r.Host, originHost(h.APIOrigin)) ||
			strings.EqualFold(originHost(h.APIOrigin), originHost(h.ShareOrigin)) {
			apierr.Write(w, r, apierr.NotFound("artifact"))
			return
		}
		sess, err := h.Sessions.Load(r.Context(), w, r)
		if errors.Is(err, auth.ErrNoSession) {
			http.Redirect(w, r, h.APIOrigin+"/app/login", http.StatusFound)
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

func (h *Handler) artifactCacheHeaders(w http.ResponseWriter, r *http.Request) {
	if isPrivateViewer(r) {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		// A public artifact may become private. Revalidate every read, including
		// pinned versions and covers, so no shared cache can bypass authorization.
		w.Header().Set("Cache-Control", "no-cache")
	}
}
