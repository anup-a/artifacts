package api

import (
	"errors"
	"net/http"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/pageurl"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

// hostKind is what a request's Host asks for.
type hostKind int

const (
	// hostOwn is byagent's own origins and any Host nobody configured (a
	// health check by address, the platform's own name for the app): the
	// normal routes.
	hostOwn hostKind = iota
	// hostContentApex is the content domain itself, which only redirects
	// to the share origin.
	hostContentApex
	// hostPage is one page's own host.
	hostPage
	// hostNowhere is a name under the content domain that is no page.
	hostNowhere
)

// hostTarget is resolveHost's answer: the kind, and the page for hostPage.
type hostTarget struct {
	kind hostKind
	page store.Artifact
}

// resolveHost is the one place a request's Host decides what serves it.
//
// Custom domains (feat/custom-domains) extend it: a connected domain is one
// more kind here, resolved after the content domain, whose mounts then pick
// the page by path. Nothing else should look at r.Host to pick a page.
func (s *server) resolveHost(r *http.Request) (hostTarget, error) {
	kind, slug := s.pages().Classify(r.Host)
	switch kind {
	case pageurl.Apex:
		return hostTarget{kind: hostContentApex}, nil
	case pageurl.Stray:
		return hostTarget{kind: hostNowhere}, nil
	case pageurl.Page:
		a, err := s.Store.ArtifactBySlug(r.Context(), slug)
		if errors.Is(err, store.ErrNotFound) {
			return hostTarget{kind: hostNowhere}, nil
		}
		if err != nil {
			return hostTarget{}, err
		}
		return hostTarget{kind: hostPage, page: a}, nil
	}
	return hostTarget{kind: hostOwn}, nil
}

// hosts routes by Host. On byagent's own hosts it is the normal mux; on a
// page host it is a small mux that serves that page and nothing else. The
// API, the dashboard, the agent pages and every other page are unreachable
// there: a path naming another page's id answers 404 (/c/{other}/...), and
// any other path is looked up among this page's own files.
//
// Every state-changing request on a page host must come from the page
// itself (serve.CrossPageWrite): page hosts are same-site with each other
// until the content domain is on the Public Suffix List.
func (s *server) hosts(next http.Handler, sh *serve.Handler) http.Handler {
	if !s.pages().PerPage() {
		return next
	}
	pm := http.NewServeMux()
	pm.HandleFunc("GET /_rt/comments.js", sh.ServeRuntime)
	pm.HandleFunc("POST /c/{id}/comments", thisPage(noStore(s.viewerCreateThread)))
	pm.HandleFunc("GET /c/{id}/comments", thisPage(noStore(s.viewerList)))
	pm.HandleFunc("GET /c/{id}/meta", thisPage(noStore(s.viewerMeta)))
	pm.HandleFunc("POST /c/{id}/comments/{thread}/reply", thisPage(noStore(s.viewerReply)))
	pm.HandleFunc("POST /c/{id}/comments/{thread}/seen", thisPage(noStore(s.viewerSeen)))
	pm.HandleFunc("/", sh.ServePage)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, err := s.resolveHost(r)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		switch t.kind {
		case hostOwn:
			next.ServeHTTP(w, r)
		case hostContentApex:
			// byagent.page itself is no page; send people to the site, path
			// kept so byagent.page/a/{id}/ still finds its page.
			w.Header().Set("Cache-Control", "public, max-age=3600")
			http.Redirect(w, r, s.Cfg.ShareOrigin+r.URL.RequestURI(), http.StatusPermanentRedirect)
		case hostPage:
			r = serve.WithPageHost(r, serve.PageHost{Origin: s.pages().Origin(t.page.Slug), ArtifactID: t.page.ID})
			if serve.CrossPageWrite(r) {
				w.Header().Set("Cache-Control", "no-store")
				apierr.Write(w, r, apierr.Forbidden("requests to a page must come from the page itself"))
				return
			}
			pm.ServeHTTP(w, r)
		default:
			w.Header().Set("Cache-Control", "no-store")
			apierr.Write(w, r, apierr.NotFound("page"))
		}
	})
}

// thisPage admits a viewer comment route on a page host only for that
// page's own id. The routes name the id for the runtime's sake; on a page
// host any other id is another page, which this host never answers for.
func thisPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p, ok := serve.PageHostOf(r); !ok || r.PathValue("id") != p.ArtifactID {
			apierr.Write(w, r, apierr.NotFound("artifact"))
			return
		}
		next(w, r)
	}
}
