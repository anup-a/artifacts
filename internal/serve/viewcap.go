package serve

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/store"
)

// A private page's own requests carry no session. The viewer is sandboxed
// (see privateArtifact), so the document has an opaque origin, and the
// browser counts everything it asks the app origin for (its images, its
// stylesheet, its scripts) as cross-site and leaves the SameSite=Lax session
// cookie off. Loosening the cookie would hand the session to every site, so
// the authorization travels in the address instead: a signed-in owner who
// opens a private HTML page is redirected to the same page under
//
//	/app/view/{id}/~cap/{capability}/{path}
//
// and the page's relative addresses resolve under that prefix by themselves,
// with no rewriting of what the owner published.
//
// A capability names one artifact, the version the viewer opened (0 for
// whatever is current, as on /app/view/{id}/; n for /app/view/{id}/v/{n}/),
// the workspace that owned it when it was minted, and an expiry a day out.
// It is signed with the server's key, so it cannot be made for another
// artifact or version, and a page moved to another workspace stops honouring
// it. It opens files and nothing else: no cover, no other artifact, no API.
//
// It is still a bearer token in the address bar, so it does not admit the
// one request a pasted link turns into: a top-level navigation
// (Sec-Fetch-Dest: document). That request carries the Lax session cookie
// anyway, so it is checked against the session instead, and a stranger who
// is handed the address is refused just as on /app/view/{id}/. A navigation
// on a lapsed capability with a valid session gets a fresh one, so a reload
// or a link clicked a day later still works for the owner.
const (
	viewCapSegment = "~cap"
	viewCapTTL     = 24 * time.Hour
)

type viewCapKey struct{}

// onViewCap reports whether this request is authorized by a live capability
// for the artifact it asks for, rather than by a session alone.
func onViewCap(r *http.Request) bool {
	ok, _ := r.Context().Value(viewCapKey{}).(bool)
	return ok
}

// mintsViewCaps reports whether this server can sign capabilities. Without a
// key they would be forgeable, so the private viewer falls back to serving
// pages in place, with their relative assets unavailable.
func (h *Handler) mintsViewCaps() bool { return h.UnlockSecret != "" }

// viewCap signs a capability for version of a (0 for current) until exp.
func (h *Handler) viewCap(a store.Artifact, version int, exp time.Time) string {
	v, e := strconv.Itoa(version), strconv.FormatInt(exp.Unix(), 10)
	return v + "." + e + "." + viewCapMAC(h.UnlockSecret, a, v, e)
}

func viewCapMAC(secret string, a store.Artifact, version, exp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// The label keeps this MAC from ever equalling another one made with the
	// same key (unlock cookies, access grants, API keys).
	mac.Write([]byte("byagent/view-cap/v1\x00" + a.ID + "\x00" + a.TenantID + "\x00" + version + "\x00" + exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// readViewCap checks tok against artifact a. ok means it was signed by this
// server for a as a stands now; expiry is left to the caller, which still
// learns the version from a lapsed capability.
func (h *Handler) readViewCap(tok string, a store.Artifact) (version int, exp time.Time, ok bool) {
	if !h.mintsViewCaps() {
		return 0, time.Time{}, false
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return 0, time.Time{}, false
	}
	if !hmac.Equal([]byte(parts[2]), []byte(viewCapMAC(h.UnlockSecret, a, parts[0], parts[1]))) {
		return 0, time.Time{}, false
	}
	v, err := strconv.Atoi(parts[0])
	if err != nil || v < 0 {
		return 0, time.Time{}, false
	}
	e, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, time.Time{}, false
	}
	return v, time.Unix(e, 0), true
}

// redirectToViewCap sends the owner from a private HTML page to the same page
// under a fresh capability. version is the one the address named (0 for
// current) and rest the file path after the version, as requested.
func (h *Handler) redirectToViewCap(w http.ResponseWriter, r *http.Request, a store.Artifact, version int, rest string) {
	target := "/app/view/" + a.ID + "/" + viewCapSegment + "/" + h.viewCap(a, version, h.now().Add(viewCapTTL)) + "/" +
		(&url.URL{Path: rest}).EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusSeeOther)
}

// capArtifact serves GET /app/view/{id}/~cap/{cap}/{path...}: what a private
// page asks for while it is open, and the page itself. It carries the same
// headers and the same sandbox as privateArtifact, and every refusal goes
// through denyPrivate, so a wrong capability, a missing session and an
// unknown id cannot be told apart.
func (h *Handler) capArtifact(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !strings.EqualFold(r.Host, originHost(h.APIOrigin)) || !h.separateOrigins() {
		apierr.Write(w, r, apierr.NotFound("artifact"))
		return
	}
	id := r.PathValue("id")
	a, err := h.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		h.denyPrivate(w, r)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	version, exp, ok := h.readViewCap(r.PathValue("cap"), a)
	if !ok {
		h.denyPrivate(w, r)
		return
	}
	live := h.now().Before(exp)
	tenant := a.TenantID
	if !live || r.Header.Get("Sec-Fetch-Dest") == "document" {
		sess, err := h.Sessions.Load(r.Context(), w, r)
		if errors.Is(err, auth.ErrNoSession) {
			h.denyPrivate(w, r)
			return
		}
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		// Another workspace's session is refused by canView, as on the
		// session-only path.
		tenant = sess.TenantID
	}
	ctx := context.WithValue(r.Context(), privateViewerKey{}, tenant)
	ctx = context.WithValue(ctx, viewCapKey{}, live && tenant == a.TenantID)
	r = r.WithContext(ctx)
	h.securityHeaders(w, r)
	h.serve(w, r, id, version, r.PathValue("path"))
}
