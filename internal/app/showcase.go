package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// showcaseLimit caps the pages on /showcase, and showcaseThreads the
// threads drawn under each one: enough to see the loop, not the whole page.
const (
	showcaseLimit   = 12
	showcaseThreads = 2
	// showcaseMinComment hides throwaway comments ("test", "1") that say
	// nothing about the page to a visitor.
	showcaseMinComment = 15
)

type showcaseThread struct {
	Quote    string
	First    store.Comment  // what the reader said
	Answer   *store.Comment // the publisher's latest reply, if any
	Replies  int            // comments after the first
	Resolved bool
}

type showcaseItem struct {
	Artifact store.Artifact
	URL      string
	CoverURL string
	Threads  []showcaseThread
	Total    int // every thread on the page, open or resolved
}

type showcaseData struct {
	siteData
	Path      string // for the canonical link in doc-head
	ShareHost string // for the prompt at the foot
	Items     []showcaseItem
}

// showcase is the public gallery of real pages published with byagent, each
// with the comments readers left on it. The pages come from one shared
// collection the owner curates in the dashboard (SHOWCASE_COLLECTION), so
// what appears here is chosen, never every public page on the server. Only
// public pages show, and only what their own readers can already see.
func (h *Handler) showcase(w http.ResponseWriter, r *http.Request) {
	site := h.site()
	site.Here = "/showcase"
	d := showcaseData{siteData: site, Path: "/showcase", ShareHost: originHost(h.Cfg.ShareOrigin)}
	if ids := h.Cfg.ShowcasePages; len(ids) > 0 {
		items, err := h.showcasePicked(r, ids)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		d.Items = items
	} else if slug := h.Cfg.ShowcaseSlug; slug != "" {
		items, err := h.showcaseItems(r, slug)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		d.Items = items
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	h.render(w, r, 200, "showcase", "Showcase", nil, d)
}

// showcasePicked loads the pages SHOWCASE_PAGES names, in that order,
// skipping any that are gone or no longer public.
func (h *Handler) showcasePicked(r *http.Request, ids []string) ([]showcaseItem, error) {
	var list []store.Artifact
	for _, id := range ids {
		a, err := h.Store.Artifact(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return h.showcaseFrom(r, list)
}

func (h *Handler) showcaseItems(r *http.Request, slug string) ([]showcaseItem, error) {
	ctx := r.Context()
	meta, err := h.Store.CollectionByShareSlug(ctx, slug)
	if errors.Is(err, store.ErrNotFound) {
		// Unshared or renamed: an empty showcase, not a broken page.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list, err := h.Store.ListArtifacts(ctx, meta.TenantID, store.ArtifactFilter{Project: meta.Name, Limit: collectionListLimit})
	if err != nil {
		return nil, err
	}
	return h.showcaseFrom(r, store.CourseOrder(list, meta.Positions))
}

func (h *Handler) showcaseFrom(r *http.Request, list []store.Artifact) ([]showcaseItem, error) {
	var (
		items []showcaseItem
		err   error
	)
	for _, a := range list {
		if a.Visibility != "public" {
			continue
		}
		it := showcaseItem{Artifact: a, URL: h.shareURL(a), CoverURL: h.pages().Cover(a.ID, a.Slug)}
		if a.CommentsEnabled {
			if it.Threads, it.Total, err = h.showcaseThreads(r, a.ID); err != nil {
				return nil, err
			}
		}
		items = append(items, it)
		if len(items) == showcaseLimit {
			break
		}
	}
	return items, nil
}

// showcaseThreads picks the threads that show the loop best: ones the
// publisher answered come first, then the newest.
func (h *Handler) showcaseThreads(r *http.Request, artifactID string) ([]showcaseThread, int, error) {
	threads, err := h.Store.Threads(r.Context(), artifactID, "", time.Time{}, 0)
	if err != nil || len(threads) == 0 {
		return nil, 0, err
	}
	ids := make([]string, len(threads))
	for i, t := range threads {
		ids[i] = t.ID
	}
	comments, err := h.Store.Comments(r.Context(), ids)
	if err != nil {
		return nil, 0, err
	}
	var answered, rest []showcaseThread
	for i := len(threads) - 1; i >= 0; i-- {
		t := threads[i]
		cs := comments[t.ID]
		if len(cs) == 0 || len([]rune(strings.TrimSpace(cs[0].Body))) < showcaseMinComment {
			continue
		}
		st := showcaseThread{Quote: anchorQuote(t.AnchorJSON), First: cs[0], Replies: len(cs) - 1, Resolved: t.Status == "resolved"}
		for j := len(cs) - 1; j > 0; j-- {
			if cs[j].AuthorKind == "owner" {
				c := cs[j]
				st.Answer = &c
				break
			}
		}
		if st.Answer != nil {
			answered = append(answered, st)
		} else {
			rest = append(rest, st)
		}
	}
	picked := append(answered, rest...)
	if len(picked) > showcaseThreads {
		picked = picked[:showcaseThreads]
	}
	return picked, len(threads), nil
}
