package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/domains"
	"github.com/anup-a/byagent/internal/pageurl"
	"github.com/anup-a/byagent/internal/serve"
	"github.com/anup-a/byagent/internal/store"
	"github.com/anup-a/byagent/internal/telemetry"
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
	// hostCustom is a connected custom domain, whose mounts pick the page
	// by path.
	hostCustom
)

// hostTarget is resolveHost's answer: the kind, the page for hostPage and
// the site for hostCustom.
type hostTarget struct {
	kind hostKind
	page store.Artifact
	site *domains.Site
}

// resolveHost is the one place a request's Host decides what serves it.
//
// The content domain is resolved first; a connected custom domain after it
// (hostCustom), whose mounts then pick the page by path. Nothing else should
// look at r.Host to pick a page.
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
	if s.Domains != nil {
		if site := s.siteForHost(r, s.ownHosts()); site != nil {
			return hostTarget{kind: hostCustom, site: site}, nil
		}
	}
	return hostTarget{kind: hostOwn}, nil
}

// ownHosts are byagent's own host names, never looked up as custom domains.
func (s *server) ownHosts() map[string]bool {
	return map[string]bool{hostOf(s.Cfg.ShareOrigin): true, hostOf(s.Cfg.APIOrigin): true}
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
	if !s.pages().PerPage() && s.Domains == nil {
		return next
	}
	cm := s.customMux(sh)
	pm := http.NewServeMux()
	pm.HandleFunc("GET /_rt/comments.js", sh.ServeRuntime)
	pm.HandleFunc("POST /c/{id}/comments", thisPage(noStore(s.viewerCreateThread)))
	pm.HandleFunc("GET /c/{id}/comments", thisPage(noStore(s.viewerList)))
	pm.HandleFunc("GET /c/{id}/meta", thisPage(noStore(s.viewerMeta)))
	pm.HandleFunc("POST /c/{id}/comments/{thread}/reply", thisPage(noStore(s.viewerReply)))
	pm.HandleFunc("POST /c/{id}/comments/{thread}/seen", thisPage(noStore(s.viewerSeen)))
	pm.HandleFunc("/", sh.ServePage)
	pages, custom := telemetry.Routed(pm), telemetry.Routed(cm)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, err := s.resolveHost(r)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		switch t.kind {
		case hostOwn:
			if pageWrite(r, s.pages()) {
				w.Header().Set("Cache-Control", "no-store")
				apierr.Write(w, r, apierr.Forbidden("pages may not make requests to byagent"))
				return
			}
			next.ServeHTTP(w, r)
		case hostContentApex:
			// A separate content domain (byagent.page) is no page itself; send
			// people to the site, path
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
			pages.ServeHTTP(w, r)
		case hostCustom:
			// A custom domain is same-site with whatever its owner runs on
			// the parent domain, so every write there must come from the
			// domain itself, as on a page host.
			if serve.CrossOriginWrite(r, s.Domains.Scheme+"://"+t.site.Domain.Hostname) {
				w.Header().Set("Cache-Control", "no-store")
				apierr.Write(w, r, apierr.Forbidden("requests to a page must come from the page itself"))
				return
			}
			telemetry.SetHostKind(r.Context(), telemetry.HostCustomDomain)
			custom.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), siteKey{}, t.site)))
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

// pageWrite reports a state-changing request to byagent's own hosts that a
// page host sent. With pages on {slug}.byagent.dev every page is same-site
// with byagent.dev and app.byagent.dev, so SameSite=Lax cookies ride along
// on its form posts; the dashboard's CSRF token stops those, and this stops
// every other write (sign-in mail, the API, legacy comment routes) before
// it starts. Nothing byagent serves on a page host posts to its own hosts.
func pageWrite(r *http.Request, b pageurl.Builder) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return b.IsPageOrigin(r.Header.Get("Origin"))
}
