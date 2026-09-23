package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/store"
)

// maxCollectionArtifacts caps one collection read at the largest page
// ListArtifacts serves.
const maxCollectionArtifacts = 200

// collectionShareURL is the public index of a shared collection, or nil when
// it is not shared.
func (s *server) collectionShareURL(slug string) any {
	if slug == "" {
		return nil
	}
	return s.Cfg.ShareOrigin + "/s/" + slug + "/"
}

// collectionFields are the metadata fields shared by both collection shapes.
func (s *server) collectionFields(c store.Collection, count int, activeAt time.Time) map[string]any {
	return map[string]any{
		"name": c.Name, "count": count, "description": c.Description,
		"updated_at": activeAt, "share_url": s.collectionShareURL(c.ShareSlug),
	}
}

// artifactViews renders artifacts exactly as the artifact endpoints do.
func (s *server) artifactViews(r *http.Request, rows []store.Artifact) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		v, err := s.artifactView(r, a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// listCollections serves GET /api/collections: every project label of the
// workspace as a collection, most recently active first, each with its
// newest artifacts.
func (s *server) listCollections(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	rows, err := s.Store.Collections(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		latest, err := s.artifactViews(r, c.Latest)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		v := s.collectionFields(c.Collection, c.Count, c.ActiveAt)
		v["latest"] = latest
		out = append(out, v)
	}
	apierr.WriteJSON(w, 200, map[string]any{"collections": out})
}

// getCollection serves GET /api/collections/{name}: one collection's metadata
// and its artifacts in course order. A label with no live artifacts and no
// saved metadata is not a collection.
func (s *server) getCollection(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	name := strings.TrimSpace(r.PathValue("name"))
	meta, err := s.Store.Collection(r.Context(), k.TenantID, name)
	saved := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	rows, err := s.Store.ListArtifacts(r.Context(), k.TenantID, store.ArtifactFilter{Project: name, Limit: maxCollectionArtifacts})
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if !saved && len(rows) == 0 {
		apierr.Write(w, r, apierr.NotFound("collection"))
		return
	}
	meta.Name = name
	activeAt := meta.UpdatedAt
	if len(rows) > 0 {
		activeAt = rows[0].UpdatedAt // ListArtifacts is newest-updated first
	}
	arts, err := s.artifactViews(r, courseOrder(rows, meta.Positions))
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	v := s.collectionFields(meta, len(rows), activeAt)
	v["artifacts"] = arts
	apierr.WriteJSON(w, 200, v)
}

// courseOrder returns a new slice in the collection's reading order: the
// saved positions first, in their order, then every other artifact by
// publish time (created_at ascending, ties by id). Positions naming an
// artifact that is not in rows, or naming one twice, are skipped.
func courseOrder(rows []store.Artifact, positions []string) []store.Artifact {
	rest := append([]store.Artifact(nil), rows...)
	sort.SliceStable(rest, func(i, j int) bool {
		if !rest[i].CreatedAt.Equal(rest[j].CreatedAt) {
			return rest[i].CreatedAt.Before(rest[j].CreatedAt)
		}
		return rest[i].ID < rest[j].ID
	})
	byID := make(map[string]store.Artifact, len(rows))
	for _, a := range rows {
		byID[a.ID] = a
	}
	out := make([]store.Artifact, 0, len(rows))
	placed := map[string]bool{}
	for _, id := range positions {
		if a, ok := byID[id]; ok && !placed[id] {
			placed[id] = true
			out = append(out, a)
		}
	}
	for _, a := range rest {
		if !placed[a.ID] {
			out = append(out, a)
		}
	}
	return out
}
