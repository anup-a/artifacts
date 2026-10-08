package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/pageurl"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/skill"
)

type Handler struct {
	Sessions *auth.Sessions
	// Deny renders the browser page for a refused /app/view address. Nil on a
	// server with no web app, which falls back to the API's 404.
	Deny        http.HandlerFunc
	Store       store.Store
	Blobs       blob.Store
	Runtime     []byte
	ShareOrigin string
	// APIOrigin hosts the dashboard, which previews published pages in an
	// iframe. It is the only origin besides the share origin allowed to frame.
	APIOrigin string
	// OnView, when set, is called once per request that served an HTML page
	// of an artifact's current version (200 or 304) to anyone but the owner
	// in the dashboard. Gates, pinned versions and assets never reach it.
	// path is the file within the version, such as "index.html"; the hook
	// decides whether the request is a person (see usage.Counter.Record).
	OnView func(r *http.Request, artifactID, path string)
	// Gate renders the page that asks a viewer for an artifact's share code.
	// Nil leaves code-shared artifacts reachable only by the one-click link.
	Gate func(w http.ResponseWriter, r *http.Request, g GateView)
	// UnlockSecret keys the unlock cookie. Empty still works — the cookie is
	// then only as unguessable as the code — so set it to KeySigningSecret.
	UnlockSecret string
	// Routes runs pages' proxy routes; nil leaves them off.
	Routes *Routes
	// Now is the clock the unlock guard meters attempts against.
	Now func() time.Time
	// Mail sends a restricted page's visitors their sign-in code. Nil leaves
	// restricted pages to their owner: the gate says it cannot send mail.
	Mail auth.Mailer
	// TrustProxy reads the client address from CF-Connecting-IP or
	// X-Forwarded-For for the gate's per-client meters.
	TrustProxy bool
	// MailPerWorkspaceDay and MailPerDay cap the codes the gate mails per
	// workspace and for the whole server in a day. Zero takes the defaults.
	MailPerWorkspaceDay, MailPerDay int
	// Pages builds page addresses. With a content domain set, /a/{id}/ on
	// the share origin only redirects to the page's own host, and ServePage
	// answers there.
	Pages    pageurl.Builder
	mailSync bool // tests: send in the request instead of after it
	access   *accessGuards
	// Primary, when set, returns the custom-domain base an artifact's
	// share-origin addresses permanently redirect to (its primary domain),
	// or "" to serve them here.
	Primary     func(ctx context.Context, artifactID string) string
	unlock      *unlockGuard
	runtimeETag string
	runtimeRev  string // short content hash, cache-busts the injected script URL
}

// GateView is what the code page needs to render itself.
type GateView struct {
	ArtifactID string
	// Kind is what the gate asks for: "code" (also when empty) for a share
	// code, "password", or "email" for a restricted page, which takes an
	// address and then the code mailed to it.
	Kind string
	// Action is where the code form posts: the page's own _unlock address.
	// Base is the path the page's own addresses hang off (PageBase), which
	// the access forms post below. AssetOrigin, when set, is the origin the
	// gate's stylesheet and script load from, because the gate is standing
	// in front of a page host whose paths all belong to the page.
	Action, Base, AssetOrigin string
	// Wrong marks a submitted code that did not match, Locked a viewer who has
	// run out of attempts on this artifact for now.
	Wrong, Locked bool
	// Email kind only. Sent shows the code step for Email; Token is a mailed
	// link waiting for its button; Expired a link that no longer works;
	// NoMail a server that cannot send the code at all.
	Email           string
	Sent            bool
	Token           string
	Expired, NoMail bool
	// Busy is a password check that found no free slot: try again shortly.
	Busy bool
}

func (h *Handler) Register(mux *http.ServeMux) {
	sum := sha256.Sum256(h.Runtime)
	hexSum := hex.EncodeToString(sum[:])
	h.runtimeETag = `"` + hexSum + `"`
	h.runtimeRev = hexSum[:12]
	h.unlock = newUnlockGuard(h.Now)
	h.access = newAccessGuards(h.Now, h.MailPerWorkspaceDay, h.MailPerDay)
	mux.HandleFunc("GET /_rt/comments.js", h.runtime)
	mux.HandleFunc("GET /skill.md", h.skill)
	mux.HandleFunc("POST /a/{id}/"+unlockPath, h.publicArtifact(h.submitCode))
	mux.HandleFunc("POST /a/{id}/"+accessEmailPath, h.publicArtifact(h.requestAccessCode))
	mux.HandleFunc("POST /a/{id}/"+accessCodePath, h.publicArtifact(h.submitAccessCode))
	mux.HandleFunc("GET /a/{id}/"+accessLinkPath, h.publicArtifact(h.openAccessLink))
	mux.HandleFunc("POST /a/{id}/"+accessLinkPath, h.publicArtifact(h.confirmAccessLink))
	mux.HandleFunc("GET /a/{id}/"+coverPath, h.publicArtifact(h.cover))
	mux.HandleFunc("GET /a/{id}/v/{version}/{path...}", h.publicArtifact(h.pinned))
	mux.HandleFunc("GET /a/{id}/{path...}", h.publicArtifact(h.current))
	h.registerRoutes(mux)
	if h.Sessions != nil {
		mux.HandleFunc("GET /app/view/{id}/"+coverPath, h.privateArtifact(h.cover))
		mux.HandleFunc("GET /app/view/{id}/"+viewCapSegment+"/{cap}/{path...}", h.capArtifact)
		mux.HandleFunc("GET /app/view/{id}/v/{version}/{path...}", h.privateArtifact(h.pinned))
		mux.HandleFunc("GET /app/view/{id}/{path...}", h.privateArtifact(h.current))
	}
	mux.HandleFunc("GET /a/{id}", h.publicArtifact(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/a/"+r.PathValue("id")+"/", http.StatusMovedPermanently)
	}))
}

func (h *Handler) runtime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", h.runtimeETag)
	if r.Header.Get("If-None-Match") == h.runtimeETag {
		w.WriteHeader(304)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write(h.Runtime)
}

// skill serves the agent skill. It is short, public and changes only with a
// deploy, so a small cache window is enough and no ETag dance is needed.
func (h *Handler) skill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write(skill.Markdown)
}

func (h *Handler) current(w http.ResponseWriter, r *http.Request) {
	h.securityHeaders(w, r)
	h.serve(w, r, r.PathValue("id"), 0, r.PathValue("path"))
}

func (h *Handler) pinned(w http.ResponseWriter, r *http.Request) {
	h.securityHeaders(w, r)
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || v < 1 {
		apierr.Write(w, r, apierr.NotFound("version"))
		return
	}
	h.serve(w, r, r.PathValue("id"), v, r.PathValue("path"))
}

func (h *Handler) securityHeaders(w http.ResponseWriter, r *http.Request) {
	policy := h.csp()
	if isPrivateViewer(r) {
		policy = strings.Replace(policy, "connect-src 'self' "+h.ShareOrigin, "connect-src 'none'", 1) + "; sandbox allow-scripts; form-action 'none'; base-uri 'none'"
	}
	if OnPageHost(r) || OnMount(r) {
		// A page on its own host (or a custom domain) talks to its own host: the comment routes
		// are there, and nothing on the share origin is the page's to call.
		// The share-code gate standing in front of it loads its stylesheet,
		// script and icon from the share origin.
		policy = strings.Replace(policy, "connect-src 'self' "+h.ShareOrigin, "connect-src 'self'", 1)
		policy = strings.Replace(policy, "script-src 'self'", "script-src 'self' "+h.ShareOrigin, 1)
		policy = strings.Replace(policy, "style-src 'self'", "style-src 'self' "+h.ShareOrigin, 1)
		policy = strings.Replace(policy, "img-src 'self'", "img-src 'self' "+h.ShareOrigin, 1)
	}
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *Handler) csp() string {
	return "default-src 'self' data: blob:; script-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net; font-src 'self' data: https://fonts.gstatic.com; " +
		"img-src 'self' data: blob: https:; connect-src 'self' " + h.ShareOrigin + "; frame-ancestors " + h.frameAncestors()
}

// frameAncestors allows the dashboard to embed a published page for preview
// and nothing else: 'self' covers the share origin, APIOrigin the dashboard.
func (h *Handler) frameAncestors() string {
	if h.APIOrigin == "" || h.APIOrigin == h.ShareOrigin {
		return "'self'"
	}
	return "'self' " + h.APIOrigin
}

// serve resolves the file for (id, version, path) and streams it. version 0 = current.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, id string, version int, p string) {
	a, err := h.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || err == nil && foreignToMount(r, a) {
		h.refuse(w, r)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	// A code on the link is spent before anything is served: it redirects to
	// the clean URL, so the address the viewer keeps never holds the code.
	// That applies even when this browser is already unlocked — otherwise
	// following the link a second time would leave the code sitting in the
	// address bar and the history of someone who never needed it again.
	//
	// Only an artifact that actually has a code is treated this way; on any
	// other artifact "k" is just a query parameter the published page owns.
	if a.DeletedAt == nil && !isPrivateViewer(r) && a.ShareCode != "" && r.URL.Query().Has(unlockParam) {
		if h.unlocked(r, a) {
			http.Redirect(w, r, PageBase(r, a.ID), http.StatusSeeOther)
			return
		}
		if h.tryUnlock(w, r, a, r.URL.Query().Get(unlockParam)) {
			return
		}
	}
	if !h.canView(r, a) {
		h.refuseOrGate(w, r, a, isPageRequest(p))
		return
	}
	if a.DeletedAt != nil {
		apierr.Write(w, r, apierr.Gone())
		return
	}
	pinnedReq := version > 0
	// What the address asked for, before index.html is filled in and the
	// current version looked up: a private page moves under a capability
	// with exactly these.
	addrVersion, rest := version, p
	if version == 0 {
		version = a.CurrentVersion
	}
	if p == "" || strings.HasSuffix(p, "/") {
		p += "index.html"
	}
	if hiddenFile(p) {
		apierr.Write(w, r, apierr.NotFound("file"))
		return
	}
	files, err := h.Store.Files(r.Context(), id, version)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	var f *store.File
	for i := range files {
		if files[i].Path == p {
			f = &files[i]
			break
		}
	}
	if f == nil {
		apierr.Write(w, r, apierr.NotFound("file"))
		return
	}
	isHTML := strings.HasPrefix(f.ContentType, "text/html")
	// A private page is only ever rendered under a capability, which is what
	// lets its own images, styles and scripts load (see viewcap.go). Other
	// files opened with the session alone are served where they are.
	if isHTML && isPrivateViewer(r) && !onViewCap(r) && h.mintsViewCaps() {
		h.redirectToViewCap(w, r, a, addrVersion, rest)
		return
	}
	// HTML gets the runtime injected only while comments are on, so the ETag
	// carries that state too: flipping the switch must not be masked by a
	// cached copy of the other variant.
	inject := isHTML && a.CommentsEnabled && !isPrivateViewer(r)
	// The root page also gets Open Graph tags carrying the title, so the tag
	// changes with a retitle as well.
	meta := isHTML && p == "index.html" && !isPrivateViewer(r)
	etag := `"` + f.SHA256 + `"`
	if inject {
		etag = `"` + f.SHA256 + "-rt-" + strconv.Itoa(version) + `"`
	} else if isHTML {
		etag = `"` + f.SHA256 + "-nort-" + strconv.Itoa(version) + `"`
	}
	if meta {
		// og:image and og:url name the host, so turning page hosts on or
		// off must not leave a cached copy pointing at the other one.
		etag = etag[:len(etag)-1] + "-" + titleHash(a.Title) + hostTag(r) + `"`
	}
	// Guest pages carry a badge until they are claimed, so the claim must
	// change the tag too.
	badge := isHTML && a.ExpiresAt != nil
	if badge {
		etag = etag[:len(etag)-1] + "-guest" + `"`
	}
	// A public root page that is not a guest page carries the byagent
	// credit instead. Pages an integrator publishes for its own customers
	// (an end user is set) and private pages, even opened with a share
	// code, are left as their owner wrote them.
	credit := meta && !badge && a.EndUserID == nil && a.Visibility != "private"
	if credit {
		etag = etag[:len(etag)-1] + "-by" + `"`
	}
	w.Header().Set("ETag", etag)
	if !pinnedReq && isHTML && !isPrivateViewer(r) && h.OnView != nil {
		h.OnView(r, id, p)
	}
	h.artifactCacheHeaders(w, r)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	rc, size, err := h.Blobs.Get(r.Context(), f.SHA256)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", f.ContentType)
	if !inject && !meta && !badge {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		io.Copy(w, rc)
		return
	}
	body, err := io.ReadAll(rc)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if meta {
		origin := h.pageOrigin(r)
		body = InjectMetaURL(body, coverTitle(a), origin+PageBase(r, id)+coverPath, origin+PageBase(r, id))
		// The byagent mark belongs on byagent's own addresses; a page on
		// its owner's domain keeps whatever icon that domain has.
		if OnPageHost(r) {
			body = InjectIconHref(body, h.ShareOrigin+"/favicon.svg")
		} else if !OnMount(r) {
			body = InjectIcon(body)
		}
	}
	if inject {
		if OnPageHost(r) || OnMount(r) {
			body = InjectBase(body, id, version, h.runtimeRev, PageBase(r, id))
		} else {
			body = Inject(body, id, version, h.runtimeRev)
		}
	}
	if badge {
		body = InjectGuestBadge(body, h.ShareOrigin)
	}
	if credit {
		body = InjectAttribution(body, h.ShareOrigin)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Write(body)
}

// coverTitle is what og:title and the generated cover show: the title, or
// the id when the artifact has none.
func coverTitle(a store.Artifact) string {
	if strings.TrimSpace(a.Title) == "" {
		return a.ID
	}
	return a.Title
}

// hostTag marks an ETag of a page served on its own host.
func hostTag(r *http.Request) string {
	if OnPageHost(r) {
		return "-h"
	}
	return ""
}

func titleHash(title string) string {
	sum := sha256.Sum256([]byte(title))
	return hex.EncodeToString(sum[:4])
}
