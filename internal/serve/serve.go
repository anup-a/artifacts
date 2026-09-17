package serve

import (
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
	// OnView, when set, is called once per page view of an artifact's current
	// index.html (200 or 304). Pinned versions and assets do not count.
	OnView func(artifactID string)
	// Gate renders the page that asks a viewer for an artifact's share code.
	// Nil leaves code-shared artifacts reachable only by the one-click link.
	Gate func(w http.ResponseWriter, r *http.Request, g GateView)
	// UnlockSecret keys the unlock cookie. Empty still works — the cookie is
	// then only as unguessable as the code — so set it to KeySigningSecret.
	UnlockSecret string
	// Now is the clock the unlock guard meters attempts against.
	Now         func() time.Time
	unlock      *unlockGuard
	runtimeETag string
	runtimeRev  string // short content hash, cache-busts the injected script URL
}

// GateView is what the code page needs to render itself.
type GateView struct {
	ArtifactID string
	// Wrong marks a submitted code that did not match, Locked a viewer who has
	// run out of attempts on this artifact for now.
	Wrong, Locked bool
}

func (h *Handler) Register(mux *http.ServeMux) {
	sum := sha256.Sum256(h.Runtime)
	hexSum := hex.EncodeToString(sum[:])
	h.runtimeETag = `"` + hexSum + `"`
	h.runtimeRev = hexSum[:12]
	h.unlock = newUnlockGuard(h.Now)
	mux.HandleFunc("GET /_rt/comments.js", h.runtime)
	mux.HandleFunc("GET /skill.md", h.skill)
	mux.HandleFunc("POST /a/{id}/"+unlockPath, h.publicArtifact(h.submitCode))
	mux.HandleFunc("GET /a/{id}/"+coverPath, h.publicArtifact(h.cover))
	mux.HandleFunc("GET /a/{id}/v/{version}/{path...}", h.publicArtifact(h.pinned))
	mux.HandleFunc("GET /a/{id}/{path...}", h.publicArtifact(h.current))
	if h.Sessions != nil {
		mux.HandleFunc("GET /app/view/{id}/"+coverPath, h.privateArtifact(h.cover))
		mux.HandleFunc("GET /app/view/{id}/v/{version}/{path...}", h.privateArtifact(h.pinned))
		mux.HandleFunc("GET /app/view/{id}/{path...}", h.privateArtifact(h.current))
	}
	mux.HandleFunc("GET /a/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/a/"+r.PathValue("id")+"/", http.StatusMovedPermanently)
	})
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
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *Handler) csp() string {
	return "default-src 'self' data: blob:; script-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; " +
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
	if errors.Is(err, store.ErrNotFound) {
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
			http.Redirect(w, r, "/a/"+a.ID+"/", http.StatusSeeOther)
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
	if version == 0 {
		version = a.CurrentVersion
	}
	if p == "" || strings.HasSuffix(p, "/") {
		p += "index.html"
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
		etag = etag[:len(etag)-1] + "-" + titleHash(a.Title) + `"`
	}
	w.Header().Set("ETag", etag)
	if !pinnedReq && p == "index.html" && h.OnView != nil {
		h.OnView(id)
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
	if !inject && !meta {
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
		body = InjectMeta(body, coverTitle(a), CoverURL(h.ShareOrigin, id))
	}
	if inject {
		body = Inject(body, id, version, h.runtimeRev)
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

func titleHash(title string) string {
	sum := sha256.Sum256([]byte(title))
	return hex.EncodeToString(sum[:4])
}
