package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/skill"
)

type Handler struct {
	Store       store.Store
	Blobs       blob.Store
	Runtime     []byte
	ShareOrigin string
	// OnView, when set, is called once per page view of an artifact's current
	// index.html (200 or 304). Pinned versions and assets do not count.
	OnView      func(artifactID string)
	runtimeETag string
}

func (h *Handler) Register(mux *http.ServeMux) {
	sum := sha256.Sum256(h.Runtime)
	h.runtimeETag = `"` + hex.EncodeToString(sum[:]) + `"`
	mux.HandleFunc("GET /_rt/comments.js", h.runtime)
	mux.HandleFunc("GET /skill.md", h.skill)
	mux.HandleFunc("GET /a/{id}/v/{version}/{path...}", h.pinned)
	mux.HandleFunc("GET /a/{id}/{path...}", h.current)
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
	h.securityHeaders(w)
	h.serve(w, r, r.PathValue("id"), 0, r.PathValue("path"))
}

func (h *Handler) pinned(w http.ResponseWriter, r *http.Request) {
	h.securityHeaders(w)
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || v < 1 {
		apierr.Write(w, r, apierr.NotFound("version"))
		return
	}
	h.serve(w, r, r.PathValue("id"), v, r.PathValue("path"))
}

func (h *Handler) securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", h.csp())
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *Handler) csp() string {
	return "default-src 'self' data: blob:; script-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; " +
		"img-src 'self' data: blob: https:; connect-src 'self' " + h.ShareOrigin + "; frame-ancestors 'none'"
}

// serve resolves the file for (id, version, path) and streams it. version 0 = current.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, id string, version int, p string) {
	a, err := h.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apierr.Write(w, r, apierr.NotFound("artifact"))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
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
	etag := `"` + f.SHA256 + `"`
	if isHTML {
		etag = `"` + f.SHA256 + "-rt-" + strconv.Itoa(version) + `"`
	}
	w.Header().Set("ETag", etag)
	if !pinnedReq && p == "index.html" && h.OnView != nil {
		h.OnView(id)
	}
	if pinnedReq {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
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
	if !isHTML {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		io.Copy(w, rc)
		return
	}
	body, err := io.ReadAll(rc)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	w.Write(Inject(body, id, version))
}
