package serve

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// unlockParam carries the code on a shared link: /a/{id}/?k=123456.
const unlockParam = "k"

// unlockPath is where the code form posts. It sits under the artifact's own
// path so the unlock cookie, scoped to that path, is sent back with it.
const unlockPath = "_unlock"

// unlockTTL is how long one unlock is remembered in the viewer's browser.
// A reader comes back to a shared page over a few days without retyping;
// past that they ask the person who sent it, who may no longer want them in.
const unlockTTL = 14 * 24 * time.Hour

// A six-digit code is a million possibilities. That is only out of reach
// because guessing is metered here, at the artifact, rather than at the
// client: an attacker with a thousand addresses is still one attacker
// working through one artifact's code space.
const (
	unlockMaxAttempts = 10
	unlockWindow      = 15 * time.Minute
)

// unlockCookie is the kind of cookie an unlock is kept in (see cookie.go).
const unlockCookie = "artifact_unlock"

// unlockCookieName is the share origin's name for it: per artifact, so one
// browser can hold codes for several artifacts at once without them
// colliding. A page host needs no id in the name; the host is the page.
func unlockCookieName(id string) string { return unlockCookie + "_" + id }

// normalizeCode makes a retyped code match a stored one. People paste it with
// spaces around it, or with the grouping punctuation a chat client added.
func normalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// codeMatches compares in constant time so a wrong code cannot be walked digit
// by digit from response timing.
func codeMatches(stored, given string) bool {
	s, g := normalizeCode(stored), normalizeCode(given)
	if s == "" || g == "" {
		return false
	}
	return hmac.Equal([]byte(s), []byte(g))
}

// unlockValue is what the cookie holds: an HMAC of the artifact id and the code
// that opened it, never the code itself. A copied cookie therefore does not
// hand over the code, and rotating the code invalidates every cookie minted
// under the old one with nothing to expire or clean up.
func (h *Handler) unlockValue(a store.Artifact) string {
	return unlockValue(h.UnlockSecret, a)
}

func unlockValue(secret string, a store.Artifact) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(a.ID + "." + normalizeCode(a.ShareCode)))
	return hex.EncodeToString(mac.Sum(nil))
}

// unlocked reports whether the request already carries a valid unlock cookie.
func (h *Handler) unlocked(r *http.Request, a store.Artifact) bool {
	return Unlocked(r, a, h.UnlockSecret)
}

// Unlocked is the same check for callers outside this package — the viewer
// comment routes, which must admit exactly the readers the page was served to
// and nobody else.
//
// A password or restricted page is unlocked by its own grant instead; such a
// page never has a share code.
func Unlocked(r *http.Request, a store.Artifact, secret string) bool {
	if a.AccessMode == AccessPassword || a.AccessMode == AccessRestricted {
		_, ok := accessGrant(r, a, secret, time.Now())
		return ok
	}
	if a.ShareCode == "" {
		return false
	}
	v := pageCookie(r, unlockCookie, a.ID)
	if v == "" {
		return false
	}
	return hmac.Equal([]byte(v), []byte(unlockValue(secret, a)))
}

// setUnlockCookie remembers the unlock for this artifact only: host-only on
// the page's own host, path-scoped on the share origin (see cookie.go).
//
// The code cannot simply stay in the query string: the page's own requests for
// its stylesheet, images and pinned assets are relative, carry no query, and
// would be refused. Moving it into a cookie also takes the code out of the
// address bar, the browser history, and any screenshot of the open page.
func (h *Handler) setUnlockCookie(w http.ResponseWriter, r *http.Request, a store.Artifact) {
	h.setPageCookie(w, r, unlockCookie, a.ID, h.unlockValue(a), int(unlockTTL.Seconds()))
}

// clearUnlockCookie drops a cookie that no longer opens anything, so a viewer
// whose code was rotated meets the gate again instead of a bare refusal.
func (h *Handler) clearUnlockCookie(w http.ResponseWriter, r *http.Request, id string) {
	h.clearPageCookie(w, r, unlockCookie, id)
}

// isPageRequest reports whether this path is a document a person navigated to,
// rather than an asset the document pulled in. The gate belongs only on the
// first kind.
func isPageRequest(p string) bool {
	return p == "" || strings.HasSuffix(p, "/") || strings.HasSuffix(p, ".html")
}

// tryUnlock handles a code arriving on the request, from either door: the
// query parameter of a one-click link, or the form on the gate page. It
// answers whether the caller has already written a response.
//
// A correct code always redirects to the clean artifact URL rather than
// rendering in place. That is what strips the code from the address bar, and
// it also means the page a viewer lands on is byte-identical to the one an
// owner sees, with no unlock state threaded through the render path.
func (h *Handler) tryUnlock(w http.ResponseWriter, r *http.Request, a store.Artifact, given string) bool {
	if a.ShareCode == "" || given == "" {
		return false
	}
	if !h.unlock.allow(a.ID) {
		h.renderGate(w, r, GateView{ArtifactID: a.ID, Locked: true})
		return true
	}
	if !codeMatches(a.ShareCode, given) {
		h.unlock.fail(a.ID)
		h.renderGate(w, r, GateView{ArtifactID: a.ID, Wrong: true})
		return true
	}
	h.unlock.succeed(a.ID)
	h.setUnlockCookie(w, r, a)
	http.Redirect(w, r, PageBase(r, a.ID), http.StatusSeeOther)
	return true
}

// submitCode takes the gate form's code, or its password on a password page.
func (h *Handler) submitCode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := h.Store.Artifact(r.Context(), id)
	if err != nil || a.DeletedAt != nil || (a.ShareCode == "" && a.AccessMode != AccessPassword) || foreignToMount(r, a) {
		h.refuse(w, r)
		return
	}
	if a.AccessMode == AccessPassword {
		if crossSite(r) {
			h.refuse(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			h.renderGate(w, r, GateView{ArtifactID: id, Kind: "password", Wrong: true})
			return
		}
		h.tryPassword(w, r, a, r.PostFormValue("password"))
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderGate(w, r, GateView{ArtifactID: id, Wrong: true})
		return
	}
	if !h.tryUnlock(w, r, a, r.PostFormValue("code")) {
		h.renderGate(w, r, GateView{ArtifactID: id, Wrong: true})
	}
}

// renderGate shows the code page, or refuses when no gate renderer is wired.
func (h *Handler) renderGate(w http.ResponseWriter, r *http.Request, g GateView) {
	if h.Gate == nil {
		h.refuse(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	g.Base = PageBase(r, g.ArtifactID)
	g.Action = g.Base + unlockPath
	if OnPageHost(r) || OnMount(r) {
		// The page host's (or custom domain's) paths are the page's; the gate's own stylesheet
		// and script come from the share origin.
		g.AssetOrigin = h.ShareOrigin
	}
	h.Gate(w, r, g)
}

// unlockGuard meters wrong codes per artifact. Per-client limiting is not
// enough here: the thing being attacked is one artifact's six digits, and an
// attacker who can vary their address would otherwise get the whole space.
// Counting at the artifact means every guess in the world shares one budget.
//
// A correct code clears the count, so the people a code was actually sent to
// are not locked out by somebody else's guessing — they simply get in.
type unlockGuard struct {
	mu   sync.Mutex
	now  func() time.Time
	seen map[string]*unlockAttempts
}

type unlockAttempts struct {
	count int
	until time.Time
}

func newUnlockGuard(now func() time.Time) *unlockGuard {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &unlockGuard{now: now, seen: map[string]*unlockAttempts{}}
}

// allow reports whether another guess may be tried for this artifact.
func (g *unlockGuard) allow(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.seen[id]
	if !ok {
		return true
	}
	if g.now().After(a.until) {
		delete(g.seen, id)
		return true
	}
	return a.count < unlockMaxAttempts
}

func (g *unlockGuard) fail(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.seen[id]
	if !ok || g.now().After(a.until) {
		a = &unlockAttempts{}
		g.seen[id] = a
	}
	a.count++
	a.until = g.now().Add(unlockWindow)
}

func (g *unlockGuard) succeed(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.seen, id)
}

// sweep drops entries whose window has passed, so a server that has refused a
// lot of guesses does not keep a row per artifact id forever.
func (g *unlockGuard) sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for id, a := range g.seen {
		if now.After(a.until) {
			delete(g.seen, id)
		}
	}
}
