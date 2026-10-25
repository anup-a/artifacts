package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/store"
)

// versionFields are the per-version fields every version listing carries
// besides the stored ones: whether it is live, when a restore last made it
// live, and where to open it.
//
// url is the pinned address, the page's address + v/{n}/. On a public page
// anyone with the link can open it, on purpose: the version switcher sends
// readers there to see the text a comment was made on. On a private page it
// is the app-origin viewer, which only signed-in members of the workspace
// can open. preview_url is always that app-origin viewer: it needs a
// dashboard sign-in to the owning workspace, runs the page in a sandbox, and
// never counts as a view. A guest has no dashboard and a server without
// sign-in has no viewer, so they get none.
func (s *server) versionFields(r *http.Request, a store.Artifact, v store.Version) map[string]any {
	pinned := "/v/" + strconv.Itoa(v.Version) + "/"
	view := s.Cfg.APIOrigin + "/app/view/" + a.ID + pinned
	url := s.pages().Version(a.ID, a.Slug, v.Version)
	if a.Visibility == "private" {
		url = view
	}
	var preview any
	if _, guest := guestFrom(r.Context()); s.Cfg.WebEnabled() && !guest {
		preview = view
	}
	return map[string]any{"current": v.Version == a.CurrentVersion, "restored_at": v.RestoredAt, "url": url, "preview_url": preview}
}

// versionParam reads the {version} path segment.
func versionParam(r *http.Request, name string) (int, *apierr.Error) {
	n, err := strconv.Atoi(r.PathValue(name))
	if err != nil || n < 1 {
		return 0, apierr.Invalid(name + " must be a positive integer")
	}
	return n, nil
}

// restoreVersion answers POST /api/artifacts/{id}/versions/{version}/restore
// (and the older /rollback/{version}): the version goes live again at once.
// It is a pointer flip, not a publish: no files are copied, no version is
// added, and access mode, share code, expiry, the comments switch, title and
// labels all stay as they are, because they belong to the page rather than
// to a version. Restoring the live version is a 409, as is restoring one
// that has been deleted or pruned (404).
func (s *server) restoreVersion(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if ok, retry := s.PublishLimiter.Allow(fmt.Sprint("key:", k.ID)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	n, e := versionParam(r, "version")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	at := s.Now()
	switch err := s.Store.RestoreVersion(r.Context(), a.ID, n, at); {
	case errors.Is(err, store.ErrNotFound):
		apierr.Write(w, r, apierr.NotFound("version"))
		return
	case errors.Is(err, store.ErrLiveVersion):
		apierr.Write(w, r, apierr.Conflict(fmt.Sprintf("v%d is already live", n)))
		return
	case err != nil:
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"id": a.ID, "url": s.artifactViewURL(a), "version": n, "current_version": n, "previous_version": a.CurrentVersion,
		"restored_at": at, "visibility": a.Visibility, "comments_enabled": a.CommentsEnabled})
}

// deleteVersion answers DELETE /api/artifacts/{id}/versions/{version}: one
// old version is gone for good, for when it held something that should not
// stay retrievable. Its pinned URL stops answering at once; its bytes are
// erased by the hourly GC unless another version or page holds the same
// file. The live version cannot be deleted (409): restore or publish
// another one first. Comments made on the deleted version are kept and
// marked orphaned.
func (s *server) deleteVersion(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if ok, retry := s.PublishLimiter.Allow(fmt.Sprint("key:", k.ID)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	n, e := versionParam(r, "version")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	orphaned, err := s.Store.DeleteVersion(r.Context(), a.ID, n)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apierr.Write(w, r, apierr.NotFound("version"))
		return
	case errors.Is(err, store.ErrLiveVersion):
		apierr.Write(w, r, apierr.Conflict(fmt.Sprintf("v%d is live; restore or publish another version before deleting it", n)))
		return
	case err != nil:
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	vs, err := s.Store.Versions(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"id": a.ID, "deleted_version": n, "current_version": a.CurrentVersion, "versions_count": len(vs), "orphaned_comments": orphaned})
}

// fileChange is one path whose bytes differ between two versions.
type fileChange struct {
	Path     string `json:"path"`
	FromSize int64  `json:"from_size"`
	ToSize   int64  `json:"to_size"`
}

type fileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// diffVersions answers GET /api/artifacts/{id}/diff?from=a&to=b (to defaults
// to the live version) with what changed between two versions, file by file:
// paths added, removed, and changed (different bytes). It compares content
// hashes, so it costs no blob reads, and it is not a text diff.
func (s *server) diffVersions(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	q := r.URL.Query()
	from, err := strconv.Atoi(q.Get("from"))
	if err != nil || from < 1 {
		apierr.Write(w, r, apierr.Invalid("from must be a positive integer"))
		return
	}
	to := a.CurrentVersion
	if v := q.Get("to"); v != "" {
		if to, err = strconv.Atoi(v); err != nil || to < 1 {
			apierr.Write(w, r, apierr.Invalid("to must be a positive integer"))
			return
		}
	}
	// Every version has at least its index.html, so no files means no version.
	before, err := s.Store.Files(r.Context(), a.ID, from)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	after, err := s.Store.Files(r.Context(), a.ID, to)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if len(before) == 0 || len(after) == 0 {
		apierr.Write(w, r, apierr.NotFound("version"))
		return
	}
	added, removed, changed, same := diffFiles(before, after)
	apierr.WriteJSON(w, 200, map[string]any{"id": a.ID, "from": from, "to": to, "added": added, "removed": removed, "changed": changed, "unchanged": same})
}

// diffFiles compares two file lists by path and content hash. The slices are
// never nil, so the JSON always has arrays; each comes out sorted by path.
func diffFiles(before, after []store.File) (added, removed []fileEntry, changed []fileChange, same int) {
	added, removed, changed = []fileEntry{}, []fileEntry{}, []fileChange{}
	old := make(map[string]store.File, len(before))
	for _, f := range before {
		old[f.Path] = f
	}
	seen := make(map[string]bool, len(after))
	for _, f := range after {
		seen[f.Path] = true
		o, ok := old[f.Path]
		switch {
		case !ok:
			added = append(added, fileEntry{f.Path, f.Size})
		case o.SHA256 != f.SHA256:
			changed = append(changed, fileChange{f.Path, o.Size, f.Size})
		default:
			same++
		}
	}
	for _, f := range before {
		if !seen[f.Path] {
			removed = append(removed, fileEntry{f.Path, f.Size})
		}
	}
	return added, removed, changed, same
}
