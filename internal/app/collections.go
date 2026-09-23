package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anup-a/artifacts/internal/cover"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

// maxDescriptionChars bounds a collection description: a paragraph, not a page.
const maxDescriptionChars = 500

// collectionListLimit is how many artifacts a collection page reads, the
// store's own list ceiling.
const collectionListLimit = 200

// deckView is one collection as a stacked deck: its newest covers, front first.
type deckView struct {
	Name     string
	URL      string
	Color    string
	Count    int
	ActiveAt time.Time
	Covers   []string
	Shared   bool
}

// mergeHint suggests folding one label into another that differs only in
// case, spacing or punctuation, like "Hi-Travel" into "Hi Travel".
type mergeHint struct {
	From, To           string
	FromCount, ToCount int
	Color              string
}

// shelfView is the collections row above the artifact stream: decks for
// collections of two or more artifacts, and any merge suggestions. Single
// artifact collections are left to the project chips and /app/collections,
// so the row does not repeat a list the page already shows.
type shelfView struct {
	Decks  []deckView
	Merges []mergeHint
	Total  int // every collection, for "View all N"
}

// shelfDecks is one row of the bento below it: the shelf never scrolls or
// cuts a deck in half, and CSS drops the last ones on narrower screens.
const shelfDecks = 4

func collectionURL(name string) string { return "/app/collections/" + url.PathEscape(name) }

func (h *Handler) publicCollectionURL(slug string) string {
	return h.Cfg.ShareOrigin + "/s/" + slug + "/"
}

func (h *Handler) deck(c store.CollectionSummary) deckView {
	d := deckView{Name: c.Name, URL: collectionURL(c.Name), Color: cover.ProjectColor(c.Name), Count: c.Count, ActiveAt: c.ActiveAt, Shared: c.ShareSlug != ""}
	for _, a := range c.Latest {
		d.Covers = append(d.Covers, h.artifactCoverURL(a))
	}
	return d
}

// labelKey folds a label to what a person would call the same name.
func labelKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mergeHints pairs labels that fold to the same key, pointing each smaller
// one at the biggest (the most recently active on a tie). Summaries arrive
// most recently active first, which the stable pick relies on.
func mergeHints(all []store.CollectionSummary) []mergeHint {
	groups := map[string][]store.CollectionSummary{}
	var keys []string
	for _, c := range all {
		k := labelKey(c.Name)
		if k == "" {
			continue
		}
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], c)
	}
	var out []mergeHint
	for _, k := range keys {
		g := groups[k]
		if len(g) < 2 {
			continue
		}
		target := g[0]
		for _, c := range g[1:] {
			if c.Count > target.Count {
				target = c
			}
		}
		for _, c := range g {
			if c.Name != target.Name {
				out = append(out, mergeHint{From: c.Name, To: target.Name, FromCount: c.Count, ToCount: target.Count, Color: cover.ProjectColor(target.Name)})
			}
		}
	}
	return out
}

func (h *Handler) shelf(all []store.CollectionSummary) *shelfView {
	s := &shelfView{Merges: mergeHints(all), Total: len(all)}
	for _, c := range all {
		if c.Count >= 2 && len(s.Decks) < shelfDecks {
			s.Decks = append(s.Decks, h.deck(c))
		}
	}
	if len(s.Decks) == 0 && len(s.Merges) == 0 {
		return nil
	}
	return s
}

type collectionsData struct {
	Decks  []deckView
	Merges []mergeHint
}

func (h *Handler) collections(w http.ResponseWriter, r *http.Request, v *viewer) {
	all, err := h.Store.Collections(r.Context(), v.Tenant.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := collectionsData{Merges: mergeHints(all)}
	for _, c := range all {
		d.Decks = append(d.Decks, h.deck(c))
	}
	h.render(w, r, 200, "collections", "Collections", v, d)
}

type collectionRow struct {
	N        int
	Artifact store.Artifact
	URL      string
	CoverURL string
}

type collectionData struct {
	Name        string
	Path        string
	Color       string
	Description string
	Count       int
	Open        int
	ActiveAt    time.Time
	Newest      bool
	Covers      []string
	Rows        []collectionRow
	ShareURL    string
}

// loadCollection reads a collection's live artifacts and saved metadata. A
// label with no live artifacts is not a collection, whatever was saved.
func (h *Handler) loadCollection(r *http.Request, tenantID, name string) ([]store.Artifact, store.Collection, error) {
	list, err := h.Store.ListArtifacts(r.Context(), tenantID, store.ArtifactFilter{Project: name, Limit: collectionListLimit})
	if err != nil {
		return nil, store.Collection{}, err
	}
	if name == "" || len(list) == 0 {
		return nil, store.Collection{}, store.ErrNotFound
	}
	meta, err := h.Store.Collection(r.Context(), tenantID, name)
	if errors.Is(err, store.ErrNotFound) {
		meta, err = store.Collection{TenantID: tenantID, Name: name, Positions: []string{}}, nil
	}
	return list, meta, err
}

func (h *Handler) collection(w http.ResponseWriter, r *http.Request, v *viewer) {
	name := r.PathValue("name")
	list, meta, err := h.loadCollection(r, v.Tenant.ID, name)
	if err != nil {
		h.storeErr(w, r, err, "That collection does not exist.")
		return
	}
	course := store.CourseOrder(list, meta.Positions)
	d := collectionData{Name: name, Path: collectionURL(name), Color: cover.ProjectColor(name), Description: meta.Description,
		Count: len(list), ActiveAt: list[0].UpdatedAt, Newest: r.URL.Query().Get("sort") == "newest"}
	if meta.ShareSlug != "" {
		d.ShareURL = h.publicCollectionURL(meta.ShareSlug)
	}
	for i := 0; i < len(course) && i < 3; i++ {
		d.Covers = append(d.Covers, h.artifactCoverURL(course[i]))
	}
	shown := course
	if d.Newest {
		shown = list
	}
	for i, a := range shown {
		d.Open += a.OpenComments
		d.Rows = append(d.Rows, collectionRow{N: i + 1, Artifact: a, URL: h.artifactURL(a), CoverURL: h.artifactCoverURL(a)})
	}
	h.render(w, r, 200, "collection", name, v, d)
}

// saveMeta loads the collection behind a POST, applies change and saves it,
// then sends the browser back to the collection page.
func (h *Handler) saveMeta(w http.ResponseWriter, r *http.Request, v *viewer, change func([]store.Artifact, *store.Collection) string) {
	name := r.PathValue("name")
	list, meta, err := h.loadCollection(r, v.Tenant.ID, name)
	if err != nil {
		h.storeErr(w, r, err, "That collection does not exist.")
		return
	}
	if msg := change(list, &meta); msg != "" {
		h.renderError(w, r, 400, msg)
		return
	}
	if err := h.Store.SaveCollection(r.Context(), meta, h.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, collectionURL(name), http.StatusFound)
}

func (h *Handler) setCollectionDescription(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.saveMeta(w, r, v, func(_ []store.Artifact, c *store.Collection) string {
		desc := strings.TrimSpace(r.PostForm.Get("description"))
		if !utf8.ValidString(desc) || utf8.RuneCountInString(desc) > maxDescriptionChars {
			return "A description can be at most 500 characters."
		}
		c.Description = desc
		return ""
	})
}

// moveCollectionItem swaps one artifact with its neighbour in course order
// and saves the whole order, so later pages still append after it.
func (h *Handler) moveCollectionItem(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.saveMeta(w, r, v, func(list []store.Artifact, c *store.Collection) string {
		course := store.CourseOrder(list, c.Positions)
		id, dir := r.PostForm.Get("id"), r.PostForm.Get("dir")
		at := -1
		for i, a := range course {
			if a.ID == id {
				at = i
			}
		}
		to := at + 1
		if dir == "up" {
			to = at - 1
		}
		if at < 0 || (dir != "up" && dir != "down") {
			return "That artifact is not in this collection."
		}
		if to >= 0 && to < len(course) {
			course[at], course[to] = course[to], course[at]
		}
		positions := make([]string, 0, len(course))
		for _, a := range course {
			positions = append(positions, a.ID)
		}
		c.Positions = positions
		return ""
	})
}

// shareCollection turns the public index on (keeping an existing link) or off.
func (h *Handler) shareCollection(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.saveMeta(w, r, v, func(_ []store.Artifact, c *store.Collection) string {
		switch {
		case r.PostForm.Get("on") == "":
			c.ShareSlug = ""
		case c.ShareSlug == "":
			c.ShareSlug = ids.NewArtifactID()
		}
		return ""
	})
}

// renameCollection relabels every artifact of the collection. Renaming onto
// an existing label merges the two.
func (h *Handler) renameCollection(w http.ResponseWriter, r *http.Request, v *viewer) {
	name := r.PathValue("name")
	if _, _, err := h.loadCollection(r, v.Tenant.ID, name); err != nil {
		h.storeErr(w, r, err, "That collection does not exist.")
		return
	}
	to, msg := cleanProject(r.PostForm.Get("to"))
	if to == nil {
		if msg == "" {
			msg = "A collection needs a name."
		}
		h.renderError(w, r, 400, msg)
		return
	}
	if _, err := h.Store.RenameCollection(r.Context(), v.Tenant.ID, name, *to, h.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, collectionURL(*to), http.StatusFound)
}

type publicCollectionData struct {
	Name        string
	Color       string
	Description string
	Rows        []collectionRow
}

// publicCollection is the shared index of a collection. It lists only public
// artifacts, in course order, and like the gate page carries no session or
// navigation: the slug is the only thing that grants it.
func (h *Handler) publicCollection(w http.ResponseWriter, r *http.Request) {
	meta, err := h.Store.CollectionByShareSlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.storeErr(w, r, err, "This collection is not shared.")
		return
	}
	list, err := h.Store.ListArtifacts(r.Context(), meta.TenantID, store.ArtifactFilter{Project: meta.Name, Limit: collectionListLimit})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := publicCollectionData{Name: meta.Name, Color: cover.ProjectColor(meta.Name), Description: meta.Description}
	for _, a := range store.CourseOrder(list, meta.Positions) {
		if a.Visibility != "public" {
			continue
		}
		d.Rows = append(d.Rows, collectionRow{N: len(d.Rows) + 1, Artifact: a, URL: h.shareURL(a.ID), CoverURL: h.shareURL(a.ID) + "_cover.svg"})
	}
	h.render(w, r, 200, "collection_public", meta.Name, nil, d)
}
