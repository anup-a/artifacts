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
	"github.com/anup-a/artifacts/internal/cover"
	"github.com/anup-a/artifacts/internal/store"
)

// coverPath is the file name under /a/{id}/ that serves the cover image.
const coverPath = "_cover.svg"

// CoverURL is the absolute cover address for an artifact on the share origin.
func CoverURL(shareOrigin, id string) string {
	return shareOrigin + "/a/" + id + "/" + coverPath
}

// shareHost is the origin without its scheme, for the cover footer.
func shareHost(origin string) string {
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	return strings.TrimSuffix(origin, "/")
}

// coverCard is the generated cover's input for an artifact.
func (h *Handler) coverCard(a store.Artifact) cover.Card {
	style := a.CoverStyle
	if !cover.Valid(style) {
		style = cover.PickStyle(a.ID)
	}
	project := ""
	if a.Project != nil {
		project = *a.Project
	}
	return cover.Card{ID: a.ID, Title: coverTitle(a), Project: project, Host: shareHost(h.ShareOrigin), PublishedAt: a.CreatedAt.UTC(), Style: style}
}

// coverETag hashes every input of a generated cover, so the tag changes with
// a retitle, a relabel or a style change and nothing else.
func coverETag(c cover.Card) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{c.ID, c.Title, c.Project, c.Host, c.PublishedAt.Format(time.RFC3339), c.Style}, "\x00")))
	return `"cov-` + hex.EncodeToString(sum[:8]) + `"`
}

// cover serves GET /a/{id}/_cover.svg: the publisher's file when cover_path
// resolves in the current version, else a generated cover. Missing and
// deleted artifacts answer like pages (404 / 410).
func (h *Handler) cover(w http.ResponseWriter, r *http.Request) {
	h.securityHeaders(w, r)
	id := r.PathValue("id")
	a, err := h.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		h.refuse(w, r)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if !h.canView(r, a) {
		h.refuse(w, r)
		return
	}
	if a.DeletedAt != nil {
		apierr.Write(w, r, apierr.Gone())
		return
	}
	if f := h.publisherCover(r, a); f != nil {
		h.streamCover(w, r, *f)
		return
	}
	card := h.coverCard(a)
	etag := coverETag(card)
	w.Header().Set("ETag", etag)
	h.artifactCacheHeaders(w, r)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	body := cover.Render(card)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Write(body)
}

// publisherCover returns the stored file named by cover_path when it exists in
// the current version. A rollback to a version without it yields nil, so the
// endpoint quietly falls back to a generated cover.
func (h *Handler) publisherCover(r *http.Request, a store.Artifact) *store.File {
	if a.CoverPath == nil || *a.CoverPath == "" {
		return nil
	}
	files, err := h.Store.Files(r.Context(), a.ID, a.CurrentVersion)
	if err != nil {
		return nil
	}
	for i := range files {
		if files[i].Path == *a.CoverPath {
			return &files[i]
		}
	}
	return nil
}

func (h *Handler) streamCover(w http.ResponseWriter, r *http.Request, f store.File) {
	etag := `"` + f.SHA256 + `"`
	w.Header().Set("ETag", etag)
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
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	io.Copy(w, rc)
}
