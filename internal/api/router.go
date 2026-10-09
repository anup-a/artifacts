package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/agents"
	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

func NewHandler(d Deps) (http.Handler, error) {
	if d.GuestLimiter == nil {
		d.GuestLimiter = ratelimit.NewWindow(5, time.Hour)
	}
	if d.ConnectLimiter == nil {
		d.ConnectLimiter = ratelimit.New(60)
	}
	if d.AccessLimiter == nil {
		d.AccessLimiter = ratelimit.New(30)
	}
	if d.InviteLimiter == nil {
		d.InviteLimiter = ratelimit.NewWindow(100, 24*time.Hour)
	}
	if d.Mail == nil {
		d.Mail = auth.MailerFor(d.Cfg.EmailProvider, d.Cfg.ResendAPIKey, d.Cfg.EmailFrom)
	}
	if d.DomainLimiter == nil {
		d.DomainLimiter = ratelimit.NewWindow(20, time.Hour)
	}
	s := &server{Deps: d}
	if d.Cfg.AdminToken != "" {
		k, err := ensureAdminKey(d)
		if err != nil {
			return nil, err
		}
		s.adminKey = &k
	}
	routes, err := s.variablesSetup()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/me", s.withAuth(s.me))
	mux.HandleFunc("POST /api/guest", s.createGuest)
	mux.HandleFunc("POST /api/connect", s.redeemConnect)
	mux.HandleFunc("POST /api/connect/device", s.startDevice)
	mux.HandleFunc("POST /api/connect/device/token", s.pollDevice)
	mux.HandleFunc("POST /api/end-users/{external_id}/portal-link", s.withAuth(s.portalLink))
	mux.HandleFunc("GET /api/artifacts", s.withAuth(s.listArtifacts))
	mux.HandleFunc("GET /api/projects", s.withAuth(s.listProjects))
	mux.HandleFunc("GET /api/collections", s.withAuth(s.listCollections))
	mux.HandleFunc("GET /api/collections/{name}", s.withAuth(s.getCollection))
	mux.HandleFunc("POST /api/artifacts/preflight", s.withAuth(s.preflight))
	mux.HandleFunc("POST /api/artifacts", s.withAuth(s.createArtifact))
	mux.HandleFunc("PUT /api/artifacts/{id}", s.withAuth(s.updateArtifact))
	mux.HandleFunc("GET /api/artifacts/{id}", s.withAuth(s.getArtifact))
	mux.HandleFunc("DELETE /api/artifacts/{id}", s.withAuth(s.deleteArtifact))
	mux.HandleFunc("GET /api/artifacts/{id}/versions", s.withAuth(s.listVersions))
	mux.HandleFunc("GET /api/artifacts/{id}/files/{path...}", s.withAuth(s.artifactFile))
	mux.HandleFunc("POST /api/artifacts/{id}/rollback/{version}", s.withAuth(s.rollback))
	mux.HandleFunc("POST /api/artifacts/{id}/versions/{version}/restore", s.withAuth(s.restoreVersion))
	mux.HandleFunc("DELETE /api/artifacts/{id}/versions/{version}", s.withAuth(s.deleteVersion))
	mux.HandleFunc("GET /api/artifacts/{id}/diff", s.withAuth(s.diffVersions))
	mux.HandleFunc("POST /api/artifacts/{id}/share-code", s.withAuth(s.setShareCode))
	mux.HandleFunc("POST /api/artifacts/{id}/expiry", s.withAuth(s.setExpiry))
	mux.HandleFunc("GET /api/artifacts/{id}/analytics", s.withAuth(s.artifactAnalytics))
	mux.HandleFunc("GET /api/analytics", s.withAuth(s.accountAnalytics))
	mux.HandleFunc("GET /api/artifacts/{id}/access", s.withAuth(s.getAccess))
	mux.HandleFunc("PATCH /api/artifacts/{id}/access", s.withAuth(s.patchAccess))
	mux.HandleFunc("POST /api/artifacts/{id}/access/invites", s.withAuth(s.sendInvites))
	mux.HandleFunc("GET /api/variables", s.withAuth(s.listVariables))
	mux.HandleFunc("PUT /api/variables/{name}", s.withAuth(s.putVariable))
	mux.HandleFunc("DELETE /api/variables/{name}", s.withAuth(s.deleteVariable))
	mux.HandleFunc("POST /c/{id}/comments", noStore(s.viewerCreateThread))
	mux.HandleFunc("GET /c/{id}/comments", noStore(s.viewerList))
	mux.HandleFunc("GET /c/{id}/meta", noStore(s.viewerMeta))
	mux.HandleFunc("POST /c/{id}/comments/{thread}/reply", noStore(s.viewerReply))
	mux.HandleFunc("POST /c/{id}/comments/{thread}/seen", noStore(s.viewerSeen))
	mux.HandleFunc("GET /api/artifacts/{id}/comments", s.withAuth(s.ownerListComments))
	mux.HandleFunc("POST /api/artifacts/{id}/comments/{thread}/reply", s.withAuth(s.ownerReply))
	mux.HandleFunc("POST /api/artifacts/{id}/comments/{thread}/resolve", s.withAuth(s.ownerResolve))
	mux.HandleFunc("POST /api/artifacts/{id}/comments/{thread}/progress", s.withAuth(s.ownerProgress))
	var sessions *auth.Sessions
	if d.Cfg.WebEnabled() {
		sessions = &auth.Sessions{Store: d.Store, Now: d.Now, Secure: auth.SecureOrigin(d.Cfg.APIOrigin)}
	}
	sh := &serve.Handler{
		Sessions: sessions, Deny: d.WebDeny, Gate: d.WebGate,
		Store: d.Store, Blobs: d.Blobs, Runtime: d.Runtime,
		ShareOrigin: d.Cfg.ShareOrigin, APIOrigin: d.Cfg.APIOrigin,
		UnlockSecret: d.Cfg.KeySecret, Now: d.Now, OnView: d.OnView,
		Mail: d.Mail, TrustProxy: d.Cfg.TrustProxyHeaders,
		MailPerWorkspaceDay: d.Cfg.Limits.AccessMailPerWorkspaceDay, MailPerDay: d.Cfg.Limits.AccessMailPerDay,
		Routes: routes,
		Pages:  s.pages(),
	}
	if d.Domains != nil {
		sh.Primary = d.Domains.PrimaryURL
	}
	sh.Register(mux)
	s.registerDomains(mux)
	// The pages written for agents and crawlers: /agents, /llms.txt,
	// /robots.txt, /sitemap.xml and the rest. Public, no key.
	(&agents.Handler{Cfg: d.Cfg, GuidePage: d.WebGuide}).Register(mux)
	// The bare host root is not an API route. The browser app owns it: the
	// landing page on the share origin, the dashboard on the API origin.
	// Without the web app mounted it falls to the JSON 404 below.
	if d.Web != nil {
		d.Web(mux)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apierr.Write(w, r, apierr.NotFound("route"))
	})
	return requestID(hsts(d.Cfg.ShareOrigin, agents.LogCrawlers(wwwToApex(d.Cfg.ShareOrigin, readOnly(d.Cfg.ReadOnly, s.hosts(mux, sh)))))), nil
}

// wwwToApex sends www.<share host> to the share origin, path kept, so
// search engines see one copy of the site instead of two. GET and HEAD get
// a 301; anything else a 308, which keeps the method and body.
func wwwToApex(shareOrigin string, next http.Handler) http.Handler {
	www := "www." + hostOf(shareOrigin)
	if www == "www." {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestHost(r) != www {
			next.ServeHTTP(w, r)
			return
		}
		code := http.StatusPermanentRedirect
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			code = http.StatusMovedPermanently
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.Redirect(w, r, strings.TrimRight(shareOrigin, "/")+r.URL.RequestURI(), code)
	})
}

// hsts tells browsers to use HTTPS for a year once they have seen the site
// over it. Only when the public origin is https: TLS ends at the proxy in
// front, so the request itself cannot say. No includeSubDomains, because
// other hosts under the same domain are not this server's to commit.
func hsts(shareOrigin string, next http.Handler) http.Handler {
	if !strings.HasPrefix(shareOrigin, "https://") {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}

// readOnly refuses every write while the server is being moved. Reads,
// including page views and the health check, carry on as normal.
func readOnly(on bool, next http.Handler) http.Handler {
	if !on {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", "300")
		apierr.Write(w, r, apierr.Unavailable("byagent is moving to a new server; publishing and comments are paused for a few minutes"))
	})
}

func ensureAdminKey(d Deps) (store.Key, error) {
	hash := ids.HashKey("admin:" + d.Cfg.AdminToken)
	k, err := d.Store.KeyByHash(context.Background(), hash)
	if errors.Is(err, store.ErrNotFound) {
		return d.Store.CreateKey(context.Background(), hash, "admin@local")
	}
	return k, err
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if err := s.Blobs.Ping(r.Context()); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	w.Write([]byte("ok"))
}
