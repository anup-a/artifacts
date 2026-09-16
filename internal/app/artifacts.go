package app

import (
	"errors"
	"net/http"

	"github.com/anup-a/artifacts/internal/store"
)

const pageSize = 50

type artifactRow struct {
	Artifact store.Artifact
	URL      string
}

type artifactsData struct {
	Rows      []artifactRow
	Next      string
	EndUser   string
	Query     string
	APIOrigin string
}

func (h *Handler) shareURL(id string) string {
	return h.Cfg.ShareOrigin + "/a/" + id + "/"
}

func (h *Handler) artifacts(w http.ResponseWriter, r *http.Request, v *viewer) {
	q := r.URL.Query()
	f := store.ArtifactFilter{Limit: pageSize + 1, Query: q.Get("q")}
	if v.Integrator() {
		f.EndUser = q.Get("end_user")
	}
	if raw := q.Get("cursor"); raw != "" {
		before, id, err := store.ParseArtifactCursor(raw)
		if err != nil {
			h.renderError(w, r, 400, "That page cursor is not valid.")
			return
		}
		f.Before, f.BeforeID = before, id
	}
	list, err := h.Store.ListArtifacts(r.Context(), v.Tenant.ID, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := artifactsData{EndUser: f.EndUser, Query: f.Query, APIOrigin: h.Cfg.APIOrigin}
	if len(list) > pageSize {
		list = list[:pageSize]
		d.Next = store.ArtifactCursor(list[pageSize-1])
	}
	for _, a := range list {
		d.Rows = append(d.Rows, artifactRow{Artifact: a, URL: h.shareURL(a.ID)})
	}
	h.render(w, r, 200, "artifacts", "Artifacts", v, d)
}

func (h *Handler) deleteArtifact(w http.ResponseWriter, r *http.Request, v *viewer) {
	ctx := r.Context()
	id := r.PathValue("id")
	a, err := h.Store.Artifact(ctx, id)
	if err == nil && (a.TenantID != v.Tenant.ID || a.DeletedAt != nil) {
		err = store.ErrNotFound
	}
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	now := h.Now()
	if err := h.Store.SoftDelete(ctx, id, now); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Store.ResolveAllThreads(ctx, id, now); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err := h.Store.PruneVersions(ctx, id, 0); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/artifacts", http.StatusFound)
}

// storeErr maps a store lookup failure to a 404 page, anything else to 500.
func (h *Handler) storeErr(w http.ResponseWriter, r *http.Request, err error, notFound string) {
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, r, 404, notFound)
		return
	}
	h.fail(w, r, err)
}
