package api

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/domains"
	"github.com/anup-a/byagent/internal/serve"
)

type siteKey struct{}

func siteFrom(ctx context.Context) *domains.Site {
	s, _ := ctx.Value(siteKey{}).(*domains.Site)
	return s
}

// requestHost is the Host header as a hostname: lower case, no port, no
// trailing dot.
func requestHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

func hostOf(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// customMux serves a connected custom domain (hosts routes it here with
// the site in the context): the pages mounted on it, the comment runtime and
// the comment routes of those pages, and nothing else. A diverted request
// never reaches the API, the dashboard or another workspace's pages.
func (s *server) customMux(sh *serve.Handler) *http.ServeMux {
	cm := http.NewServeMux()
	if s.Domains == nil {
		return cm
	}
	page := s.sitePage(sh)
	cm.HandleFunc("GET /_rt/comments.js", sh.ServeRuntime)
	cm.HandleFunc("POST /c/{id}/comments", siteComments(page, noStore(s.viewerCreateThread)))
	cm.HandleFunc("GET /c/{id}/comments", siteComments(page, noStore(s.viewerList)))
	cm.HandleFunc("GET /c/{id}/meta", siteComments(page, noStore(s.viewerMeta)))
	cm.HandleFunc("POST /c/{id}/comments/{thread}/reply", siteComments(page, noStore(s.viewerReply)))
	cm.HandleFunc("POST /c/{id}/comments/{thread}/seen", siteComments(page, noStore(s.viewerSeen)))
	cm.HandleFunc("/", page)
	return cm
}

// siteForHost is resolveHost's custom-domain step: nil for byagent's own
// hosts and for any Host nobody connected (those take the normal routes),
// else the connected custom domain, whose mounts then pick the page by path
// (domains.Site.Match).
func (s *server) siteForHost(r *http.Request, own map[string]bool) *domains.Site {
	host := requestHost(r)
	if own[host] {
		return nil
	}
	site, ok := s.Domains.Index.Site(r.Context(), host)
	if !ok {
		return nil
	}
	return site
}

// siteComments admits the viewer comment routes only for a page mounted on
// this domain. Any other /c/... path is just a path, and goes to the mounts
// like every other one, so a page with its own /c/ directory still works.
func siteComments(page, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		site := siteFrom(r.Context())
		if site == nil || !site.Serving() || !site.Mounted(r.PathValue("id")) {
			page(w, r)
			return
		}
		next(w, r)
	}
}

// sitePage serves the mount a path falls under. A domain whose owner has
// not proven control of it serves nothing at all.
func (s *server) sitePage(sh *serve.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		site := siteFrom(r.Context())
		if site == nil || !site.Serving() {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("This domain is connected to byagent, but its owner has not finished setting it up.\n"))
			return
		}
		m, rest, slash, ok := site.Match(r.URL.Path)
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			apierr.Write(w, r, apierr.NotFound("page"))
			return
		}
		if slash {
			target := domains.Base(m.Path)
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		sh.ServeMount(w, r, serve.Mount{
			Scheme: s.Domains.Scheme, Host: site.Domain.Hostname, Base: domains.Base(m.Path),
			ArtifactID: m.ArtifactID, TenantID: site.Domain.TenantID,
		}, rest)
	}
}
