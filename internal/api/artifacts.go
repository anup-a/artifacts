package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/store"
)

func (s *server) artifactView(r *http.Request, a store.Artifact) (map[string]any, error) {
	vs, err := s.Store.Versions(r.Context(), a.ID)
	if err != nil {
		return nil, err
	}
	open, err := s.Store.CountOpenThreads(r.Context(), a.ID)
	if err != nil {
		return nil, err
	}
	out := s.galleryFields(a)
	for k, v := range map[string]any{
		"id": a.ID, "title": a.Title, "url": s.artifactViewURL(a), "current_version": a.CurrentVersion,
		"versions_count": len(vs), "open_comments": open, "comments_enabled": a.CommentsEnabled, "end_user": a.EndUser,
		"created_at": a.CreatedAt, "updated_at": a.UpdatedAt, "published_at": a.CreatedAt,
	} {
		out[k] = v
	}
	return out, nil
}

func (s *server) getArtifact(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	v, err := s.artifactView(r, a)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, v)
}

func (s *server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	q := r.URL.Query()
	f := store.ArtifactFilter{Limit: 50, EndUser: q.Get("end_user"), Project: strings.TrimSpace(q.Get("project"))}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			apierr.Write(w, r, apierr.Invalid("limit must be 1..200"))
			return
		}
		f.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		ts, id, err := store.ParseArtifactCursor(v)
		if err != nil {
			apierr.Write(w, r, apierr.Invalid("cursor is invalid"))
			return
		}
		f.Before, f.BeforeID = ts, id
	}
	rows, err := s.Store.ListArtifacts(r.Context(), k.TenantID, f)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		v, err := s.artifactView(r, a)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		out = append(out, v)
	}
	resp := map[string]any{"artifacts": out}
	if len(rows) == f.Limit {
		resp["next_cursor"] = store.ArtifactCursor(rows[len(rows)-1])
	}
	apierr.WriteJSON(w, 200, resp)
}

func (s *server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	now := s.Now()
	if err := s.Store.SoftDelete(r.Context(), a.ID, now); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	// Open threads protect their version from pruning, and after the delete the
	// artifact answers 410 so nobody can resolve them. Resolve them here or the
	// versions, and the blobs behind them, are retained forever.
	if err := s.Store.ResolveAllThreads(r.Context(), a.ID, now); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if _, err := s.Store.PruneVersions(r.Context(), a.ID, 0); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	w.WriteHeader(204)
}

func (s *server) listVersions(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	vs, err := s.Store.Versions(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	counts, err := s.Store.CountThreadsByVersion(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, map[string]any{"version": v.Version, "created_at": v.CreatedAt, "bytes": v.Bytes, "files": v.FileCount, "comment_count": counts[v.Version]})
	}
	apierr.WriteJSON(w, 200, map[string]any{"versions": out})
}

func (s *server) rollback(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	target, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || target < 1 {
		apierr.Write(w, r, apierr.Invalid("version must be a positive integer"))
		return
	}
	files, err := s.Store.Files(r.Context(), a.ID, target)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if len(files) == 0 {
		apierr.Write(w, r, apierr.NotFound("version"))
		return
	}
	m := manifest{Title: a.Title}
	for _, f := range files {
		m.Files = append(m.Files, manifestFile{Path: f.Path, SHA256: f.SHA256, Size: f.Size})
	}
	_, v, e := s.publishVersion(r.Context(), a, m)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"id": a.ID, "url": s.artifactViewURL(a), "version": v, "visibility": a.Visibility})
}
