package api

import (
	"net/http"
	"strconv"
	"time"

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
	return map[string]any{
		"id": a.ID, "title": a.Title, "url": s.artifactURL(a.ID), "current_version": a.CurrentVersion,
		"versions_count": len(vs), "open_comments": open, "created_at": a.CreatedAt, "updated_at": a.UpdatedAt,
	}, nil
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
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			apierr.Write(w, r, apierr.Invalid("limit must be 1..200"))
			return
		}
		limit = n
	}
	before := time.Now().UTC().Add(time.Hour)
	if v := r.URL.Query().Get("cursor"); v != "" {
		ts, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			apierr.Write(w, r, apierr.Invalid("cursor is invalid"))
			return
		}
		before = ts
	}
	rows, err := s.Store.ListArtifacts(r.Context(), k.ID, limit, before)
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
	if len(rows) == limit {
		resp["next_cursor"] = rows[len(rows)-1].CreatedAt.UTC().Format(time.RFC3339Nano)
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
	// versions — and the blobs behind them — are retained forever.
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
	v, e := s.publishVersion(r.Context(), a, m)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"id": a.ID, "url": s.artifactURL(a.ID), "version": v})
}
