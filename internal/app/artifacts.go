package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/anup-a/byagent/internal/i18n"
	"github.com/anup-a/byagent/internal/links"
	"github.com/anup-a/byagent/internal/pageurl"
	"github.com/anup-a/byagent/internal/store"
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
	// Quota is set on the unfiltered first page once the workspace is near
	// its page limit, so the warning shows before publishes start failing.
	Quota *pageQuota
	// Views is each listed artifact's views over the last 30 days.
	Views map[string]int64
	// Connect is the connect prompt shown when the workspace has nothing yet.
	Connect *promptView
	// Source is which kind of work the list shows: "" for both, "hosted"
	// for pages byagent serves, "linked" for links to work published
	// elsewhere.
	Source string
	// Links are the links matching the filters: all of them under
	// source=linked, the newest few under the default view (LinksMore says
	// there are more).
	Links     []linkRow
	LinksMore bool
	// HasLinks is whether the workspace has any link at all, which is when
	// the source switch is worth showing.
	HasLinks bool
}

// linkRow is one link in the dashboard: where it lives and who filed it.
type linkRow struct {
	Link     store.Link
	Provider string
	// Shown is the URL without its scheme, which is what a reader scans.
	Shown string
}

// linksPreview is how many links the default view shows before "View all".
const linksPreview = 6

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
	if d.Source != "" {
		q.Set("source", d.Source)
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
	return artifactsData{EndUser: d.EndUser, Query: d.Query, Project: project, Source: d.Source}.PageURL("")
}

// SourceURL is the list URL switched to one source, keeping the search and
// project filters.
func (d artifactsData) SourceURL(source string) string {
	return artifactsData{EndUser: d.EndUser, Query: d.Query, Project: d.Project, Source: source}.PageURL("")
}

// pages builds page addresses: the one helper every link to a published
// page goes through, so the dashboard agrees with the API and the page.
func (h *Handler) pages() pageurl.Builder { return pageurl.New(h.Cfg) }

// shareURL is a page's public address.
func (h *Handler) shareURL(a store.Artifact) string { return h.pages().Page(a.ID, a.Slug) }

// artifactURL keeps private pages on the account origin, where the owner's
// browser session is available even when public sharing uses another origin.
func (h *Handler) artifactURL(a store.Artifact) string {
	if a.Visibility == "private" {
		return h.Cfg.APIOrigin + "/app/view/" + a.ID + "/"
	}
	return h.shareURL(a)
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
	source := q.Get("source")
	if source != "hosted" && source != "linked" {
		source = ""
	}
	if source == "linked" {
		h.linkedArtifacts(w, r, v, f)
		return
	}
	firstPage := true
	if raw := q.Get("cursor"); raw != "" {
		before, id, err := store.ParseArtifactCursor(raw)
		if err != nil {
			h.renderError(w, r, 400, tr(r, "That page cursor is not valid."))
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
	d := artifactsData{EndUser: f.EndUser, Query: f.Query, Project: f.Project, Projects: projects, APIOrigin: h.Cfg.APIOrigin, Source: source}
	d.Filtered = d.EndUser != "" || d.Query != "" || d.Project != ""
	if d.HasLinks, err = h.hasLinks(r, v); err != nil {
		h.fail(w, r, err)
		return
	}
	// Links belong to the workspace, not to an end user, so the end-user
	// view leaves them out.
	if firstPage && source == "" && f.EndUser == "" {
		if d.Links, err = h.linkRows(r, v, store.LinkFilter{Query: f.Query, Project: f.Project, Limit: linksPreview + 1}); err != nil {
			h.fail(w, r, err)
			return
		}
		if len(d.Links) > linksPreview {
			d.Links, d.LinksMore = d.Links[:linksPreview], true
		}
	}
	if len(list) > pageSize {
		list = list[:pageSize]
		d.Next = store.ArtifactCursor(list[pageSize-1])
	}
	d.Count = len(list)
	d.Views = h.listViews(r, list)
	older := list
	if firstPage {
		var recent []store.Artifact
		recent, older = splitRecent(list, h.Now())
		if d.Filtered {
			d.Grid = h.grid(recent)
		} else {
			d.Sections = h.sections(i18n.Lang(r), recent, h.Now())
			all, err := h.Store.Collections(r.Context(), v.Tenant.ID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
			d.Shelf = h.shelf(all)
			q, err := h.pageQuota(r.Context(), v.Tenant.ID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
			if q.Near() {
				d.Quota = &q
			}
		}
	}
	for _, a := range older {
		d.Rows = append(d.Rows, artifactRow{Artifact: a, URL: h.artifactURL(a), CoverURL: h.artifactCoverURL(a)})
	}
	if firstPage && !d.Filtered && len(list) == 0 && len(d.Links) == 0 {
		d.Connect = h.mintPrompt(r, v)
	}
	h.render(w, r, 200, "artifacts", tr(r, "Artifacts"), v, d)
}

// linkedArtifacts is the list under source=linked: every link matching the
// search and project, no pages.
func (h *Handler) linkedArtifacts(w http.ResponseWriter, r *http.Request, v *viewer, f store.ArtifactFilter) {
	projects, err := h.Store.Projects(r.Context(), v.Tenant.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := artifactsData{Query: f.Query, Project: f.Project, Projects: projects, APIOrigin: h.Cfg.APIOrigin, Source: "linked"}
	d.Filtered = d.Query != "" || d.Project != ""
	d.HasLinks = true
	if d.Links, err = h.linkRows(r, v, store.LinkFilter{Query: f.Query, Project: f.Project, Limit: 200}); err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "artifacts", tr(r, "Artifacts"), v, d)
}

func (h *Handler) hasLinks(r *http.Request, v *viewer) (bool, error) {
	n, err := h.Store.CountLinks(r.Context(), v.Tenant.ID)
	return n > 0, err
}

func (h *Handler) linkRows(r *http.Request, v *viewer, f store.LinkFilter) ([]linkRow, error) {
	ls, err := h.Store.ListLinks(r.Context(), v.Tenant.ID, f)
	if err != nil {
		return nil, err
	}
	out := make([]linkRow, 0, len(ls))
	for _, l := range ls {
		shown := strings.TrimPrefix(strings.TrimPrefix(l.URL, "https://"), "http://")
		out = append(out, linkRow{Link: l, Provider: links.Provider(l.Host), Shown: strings.TrimSuffix(shown, "/")})
	}
	return out, nil
}

func (h *Handler) deleteLink(w http.ResponseWriter, r *http.Request, v *viewer) {
	err := h.Store.DeleteLink(r.Context(), v.Tenant.ID, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That link does not exist."))
		return
	}
	http.Redirect(w, r, "/app/artifacts?source=linked", http.StatusFound)
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
		h.storeErr(w, r, err, tr(r, "That artifact does not exist."))
		return
	}
	if err := h.Store.SoftDelete(ctx, id, h.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Store.Erase(ctx, id); err != nil {
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
