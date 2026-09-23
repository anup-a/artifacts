package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/anup-a/artifacts/internal/store"
)

const pageSize = 50

type artifactRow struct {
	Artifact store.Artifact
	URL      string
	CoverURL string
}

type artifactsData struct {
	// Sections are the day-grouped bento tiles of the last 7 days; Grid is the
	// same tiles as one ungrouped bento when a search or project filter is on.
	Sections []sectionView
	Grid     []cardView
	// Rows is the plain list: everything older, or every result past the
	// first page.
	Rows      []artifactRow
	Count     int // artifacts on this page, tiles and rows together
	Next      string
	EndUser   string
	Query     string
	Project   string
	Projects  []store.ProjectCount
	Filtered  bool // search, end-user or project filter active
	APIOrigin string
	// Shelf is the collections row, only on the unfiltered first page: a
	// filtered list is already one slice of the workspace.
	Shelf *shelfView
}

// PageURL is the list URL with the current filters and the given cursor, so
// pagination keeps whatever the viewer narrowed to.
func (d artifactsData) PageURL(cursor string) string {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if d.EndUser != "" {
		q.Set("end_user", d.EndUser)
	}
	if d.Query != "" {
		q.Set("q", d.Query)
	}
	if d.Project != "" {
		q.Set("project", d.Project)
	}
	if len(q) == 0 {
		return "/app/artifacts"
	}
	// %20 rather than + so html/template leaves the href readable.
	return "/app/artifacts?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}

// ChipURL is the list URL filtered to one project (or none), keeping the
// search and end-user filters.
func (d artifactsData) ChipURL(project string) string {
	return artifactsData{EndUser: d.EndUser, Query: d.Query, Project: project}.PageURL("")
}

func (h *Handler) shareURL(id string) string {
	return h.Cfg.ShareOrigin + "/a/" + id + "/"
}

// artifactURL keeps private pages on the account origin, where the owner's
// browser session is available even when public sharing uses another origin.
func (h *Handler) artifactURL(a store.Artifact) string {
	if a.Visibility == "private" {
		return h.Cfg.APIOrigin + "/app/view/" + a.ID + "/"
	}
	return h.shareURL(a.ID)
}

func (h *Handler) artifactCoverURL(a store.Artifact) string {
	return h.artifactURL(a) + "_cover.svg"
}

func (h *Handler) artifacts(w http.ResponseWriter, r *http.Request, v *viewer) {
	q := r.URL.Query()
	f := store.ArtifactFilter{Limit: pageSize + 1, Query: q.Get("q"), Project: q.Get("project")}
	if v.Integrator() {
		f.EndUser = q.Get("end_user")
	}
	firstPage := true
	if raw := q.Get("cursor"); raw != "" {
		before, id, err := store.ParseArtifactCursor(raw)
		if err != nil {
			h.renderError(w, r, 400, "That page cursor is not valid.")
			return
		}
		f.Before, f.BeforeID = before, id
		firstPage = false
	}
	list, err := h.Store.ListArtifacts(r.Context(), v.Tenant.ID, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	projects, err := h.Store.Projects(r.Context(), v.Tenant.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := artifactsData{EndUser: f.EndUser, Query: f.Query, Project: f.Project, Projects: projects, APIOrigin: h.Cfg.APIOrigin}
	d.Filtered = d.EndUser != "" || d.Query != "" || d.Project != ""
	if len(list) > pageSize {
		list = list[:pageSize]
		d.Next = store.ArtifactCursor(list[pageSize-1])
	}
	d.Count = len(list)
	older := list
	if firstPage {
		var recent []store.Artifact
		recent, older = splitRecent(list, h.Now())
		if d.Filtered {
			d.Grid = h.grid(recent)
		} else {
			d.Sections = h.sections(recent, h.Now())
			all, err := h.Store.Collections(r.Context(), v.Tenant.ID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
			d.Shelf = h.shelf(all)
		}
	}
	for _, a := range older {
		d.Rows = append(d.Rows, artifactRow{Artifact: a, URL: h.artifactURL(a), CoverURL: h.artifactCoverURL(a)})
	}
	h.render(w, r, 200, "artifacts", "Artifacts", v, d)
}

// ownedArtifact loads a live artifact that belongs to the viewer's workspace;
// anything else is ErrNotFound so other tenants' ids stay invisible.
func (h *Handler) ownedArtifact(r *http.Request, v *viewer, id string) (store.Artifact, error) {
	a, err := h.Store.Artifact(r.Context(), id)
	if err != nil {
		return a, err
	}
	if a.TenantID != v.Tenant.ID || a.DeletedAt != nil {
		return a, store.ErrNotFound
	}
	return a, nil
}

func (h *Handler) deleteArtifact(w http.ResponseWriter, r *http.Request, v *viewer) {
	ctx := r.Context()
	id := r.PathValue("id")
	if _, err := h.ownedArtifact(r, v, id); err != nil {
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
