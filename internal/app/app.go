// Package app is the browser dashboard: sign-in, artifacts, end users, API
// keys, usage, settings, and the read-only end-user portal.
package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

// Handler serves everything under /app and /p.
type Handler struct {
	Store    store.Store
	Cfg      config.Config
	Now      func() time.Time
	Sessions *auth.Sessions
	GitHub   *auth.GitHub // nil when GitHub sign-in is not configured
	Magic    *auth.MagicLinks
	// Limiters default in Register when nil: sign-in mail 5/h per address and
	// 20/h per IP, key creation 20/h per tenant, portal links 60/min per tenant.
	EmailLimiter, IPLimiter, KeyLimiter, PortalLimiter *ratelimit.Limiter

	static *statics
}

// New wires the auth pieces from the config. Callers may still swap fields
// (a fake GitHub, a capturing mail sender) before Register.
func New(st store.Store, cfg config.Config, now func() time.Time) *Handler {
	h := &Handler{Store: st, Cfg: cfg, Now: now}
	h.Sessions = &auth.Sessions{Store: st, Now: now, Secure: auth.SecureOrigin(cfg.APIOrigin)}
	if cfg.GitHubClientID != "" {
		h.GitHub = &auth.GitHub{ClientID: cfg.GitHubClientID, ClientSecret: cfg.GitHubClientSecret,
			RedirectURL: cfg.APIOrigin + "/app/login/github/callback", Store: st, Now: now}
	}
	if cfg.EmailProvider != "" {
		var send auth.Sender = auth.LogSender{}
		if cfg.EmailProvider == "resend" {
			send = auth.ResendSender{APIKey: cfg.ResendAPIKey, From: cfg.EmailFrom}
		}
		h.Magic = &auth.MagicLinks{Store: st, Now: now, Send: send, Origin: cfg.APIOrigin}
	}
	return h
}

// Register mounts the routes. Every response under /app and /p carries the
// strict headers from spec section 11.
func (h *Handler) Register(mux *http.ServeMux) {
	h.static = newStatics()
	if h.EmailLimiter == nil {
		h.EmailLimiter = ratelimit.NewWindow(5, time.Hour)
	}
	if h.IPLimiter == nil {
		h.IPLimiter = ratelimit.NewWindow(20, time.Hour)
	}
	if h.KeyLimiter == nil {
		h.KeyLimiter = ratelimit.NewWindow(20, time.Hour)
	}
	if h.PortalLimiter == nil {
		h.PortalLimiter = ratelimit.New(60)
	}
	mux.HandleFunc("GET /app/static/{name}", h.static.serve)
	mux.HandleFunc("GET /{$}", h.headers(h.landing))
	for path := range docPages {
		mux.HandleFunc("GET "+path, h.headers(h.doc(path)))
	}
	for name := range iconFiles {
		mux.HandleFunc("GET /"+name, h.static.serveIcon)
	}
	mux.HandleFunc("GET /p/{token}", h.headers(h.portal))

	mux.HandleFunc("GET /app/login", h.public(h.loginPage))
	mux.HandleFunc("GET /app/login/github", h.public(h.githubStart))
	mux.HandleFunc("GET /app/login/github/callback", h.public(h.githubCallback))
	mux.HandleFunc("POST /app/login/email", h.public(h.emailStart))
	mux.HandleFunc("GET /app/login/email/verify", h.public(h.emailVerify))

	mux.HandleFunc("GET /app/{$}", h.private(func(w http.ResponseWriter, r *http.Request, _ *viewer) {
		if resumeClaim(w, r) {
			return
		}
		http.Redirect(w, r, "/app/artifacts", http.StatusFound)
	}))
	mux.HandleFunc("GET /app/claim/{code}", h.public(h.claimPage))
	mux.HandleFunc("POST /app/claim/{code}", h.private(h.claim))
	mux.HandleFunc("GET /app/artifacts", h.private(h.artifacts))
	mux.HandleFunc("GET /app/artifacts/{id}", h.private(h.artifactDetail))
	mux.HandleFunc("POST /app/artifacts/{id}/delete", h.private(h.deleteArtifact))
	mux.HandleFunc("POST /app/artifacts/{id}/rollback/{version}", h.private(h.rollbackArtifact))
	mux.HandleFunc("POST /app/artifacts/{id}/comments-enabled", h.private(h.setCommentsEnabled))
	mux.HandleFunc("POST /app/artifacts/{id}/visibility", h.private(h.setVisibility))
	mux.HandleFunc("POST /app/artifacts/{id}/share-code", h.private(h.setShareCode))
	mux.HandleFunc("POST /app/artifacts/{id}/meta", h.private(h.setMetadata))
	mux.HandleFunc("POST /app/artifacts/{id}/comments/{thread}/resolve", h.private(h.resolveThread))
	mux.HandleFunc("POST /app/artifacts/{id}/comments/{thread}/reply", h.private(h.replyThread))
	mux.HandleFunc("GET /app/collections", h.private(h.collections))
	mux.HandleFunc("GET /app/collections/{name}", h.private(h.collection))
	mux.HandleFunc("POST /app/collections/{name}/meta", h.private(h.setCollectionDescription))
	mux.HandleFunc("POST /app/collections/{name}/move", h.private(h.moveCollectionItem))
	mux.HandleFunc("POST /app/collections/{name}/share", h.private(h.shareCollection))
	mux.HandleFunc("POST /app/collections/{name}/rename", h.private(h.renameCollection))
	mux.HandleFunc("GET /s/{slug}/{$}", h.headers(h.publicCollection))
	mux.HandleFunc("GET /s/{slug}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/s/"+url.PathEscape(r.PathValue("slug"))+"/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /app/end-users", h.private(h.endUsers))
	mux.HandleFunc("POST /app/end-users/{external_id}/portal-link", h.private(h.portalLink))
	mux.HandleFunc("GET /app/keys", h.private(h.keys))
	mux.HandleFunc("POST /app/keys", h.private(h.createKey))
	mux.HandleFunc("POST /app/keys/{id}/revoke", h.private(h.revokeKey))
	mux.HandleFunc("GET /app/usage", h.private(h.usage))
	mux.HandleFunc("GET /app/settings", h.private(h.settings))
	mux.HandleFunc("POST /app/settings/tenant", h.private(h.renameTenant))
	mux.HandleFunc("POST /app/settings/integrator", h.private(h.createIntegrator))
	mux.HandleFunc("POST /app/switch-tenant", h.private(h.switchTenant))
	mux.HandleFunc("POST /app/logout", h.private(h.logout))
	mux.HandleFunc("/app/", h.headers(func(w http.ResponseWriter, r *http.Request) {
		h.renderError(w, r, 404, "That page does not exist.")
	}))
}

// viewer is the signed-in user as seen by one request.
type viewer struct {
	Session store.Session
	User    store.User
	Tenant  store.Tenant
	Tenants []store.Tenant
}

func (v *viewer) Integrator() bool { return v.Tenant.Kind == "integrator" }
func (v *viewer) CSRF() string     { return auth.CSRFToken(v.Session.ID) }

const csp = "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'"

// csp adds the share origin as the only allowed frame and image source, so
// the artifact detail page can preview the published page and the gallery
// can show the covers served next to it. When Google Analytics is configured,
// script-src / connect-src / img-src also allow the GA4 hosts; when the ID is
// empty the header matches today's policy exactly.
func (h *Handler) csp() string {
	if h.Cfg.GoogleAnalyticsID == "" {
		return csp + "; img-src 'self' data: " + h.Cfg.ShareOrigin + "; frame-src 'self' " + h.Cfg.ShareOrigin
	}
	return "default-src 'self'; style-src 'self'; script-src 'self' https://www.googletagmanager.com https://www.google-analytics.com; frame-ancestors 'none'; img-src 'self' data: " + h.Cfg.ShareOrigin + " https://www.google-analytics.com; frame-src 'self' " + h.Cfg.ShareOrigin + "; connect-src 'self' https://www.googletagmanager.com https://www.google-analytics.com https://*.google-analytics.com https://*.analytics.google.com"
}

// headers adds the browser hardening headers to every app response.
func (h *Handler) headers(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", h.csp())
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// public serves sign-in routes: no session, but a 503 page when no provider
// is configured.
func (h *Handler) public(next http.HandlerFunc) http.HandlerFunc {
	return h.headers(func(w http.ResponseWriter, r *http.Request) {
		if !h.Cfg.WebEnabled() {
			h.renderError(w, r, 503, "Sign-in is not configured on this server.")
			return
		}
		next(w, r)
	})
}

// DenyArtifactAccess answers a private artifact address that the visitor may
// not read. It is deliberately the same page whether the artifact is private,
// belongs to someone else, or does not exist at all: anything that separated
// those cases would turn the address into a way of testing what exists.
//
// It does not bounce anyone to sign-in. Most people who land here were sent a
// link they were never going to be able to open, and a login form tells them
// the wrong thing. Signing in is offered as a way out for the owner who is
// merely signed out, not taken on their behalf.
func (h *Handler) DenyArtifactAccess(w http.ResponseWriter, r *http.Request) {
	h.headers(func(w http.ResponseWriter, r *http.Request) {
		d := errorData{
			Status:    http.StatusNotFound,
			Title:     "No access",
			Message:   "You do not have access to this artifact. It may be private to another workspace, or it may no longer exist.",
			RequestID: apierr.RequestID(r.Context()),
			SignIn:    h.Cfg.WebEnabled(),
		}
		h.renderErrorData(w, r, d)
	})(w, r)
}

// GateArtifactAccess renders the page asking a viewer for an artifact's share
// code. Unlike everything else in this package it answers on the SHARE origin,
// standing in front of a published page rather than inside the dashboard, so
// it carries no session, no navigation, and no CSRF token: the code is the
// only credential involved, and metering wrong guesses is the server's job,
// not the form's.
//
// Analytics is intentionally omitted: this unlock UI is customer-facing on the
// share origin, not a first-party app surface.
func (h *Handler) GateArtifactAccess(w http.ResponseWriter, r *http.Request, g serve.GateView) {
	hd := w.Header()
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "private, no-store")
	status := http.StatusOK
	if g.Locked {
		status = http.StatusTooManyRequests
	} else if g.Wrong {
		status = http.StatusForbidden
	}
	p := page{Title: "Enter the code", Path: r.URL.Path, CSS: h.static.cssURL, JS: h.static.jsURL, Now: h.Now(), Data: g}
	var buf bytes.Buffer
	if err := pages["gate"].ExecuteTemplate(&buf, "layout.html", p); err != nil {
		http.Error(w, "This artifact needs a code.", status)
		return
	}
	hd.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// private loads the session and, for POSTs, checks the CSRF field.
func (h *Handler) private(next func(http.ResponseWriter, *http.Request, *viewer)) http.HandlerFunc {
	return h.public(func(w http.ResponseWriter, r *http.Request) {
		v, err := h.load(r.Context(), w, r)
		if errors.Is(err, auth.ErrNoSession) {
			http.Redirect(w, r, "/app/login", http.StatusFound)
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if r.Method == "POST" {
			if err := r.ParseForm(); err != nil {
				h.renderError(w, r, 400, "The form could not be read.")
				return
			}
			if !auth.CheckCSRF(v.Session.ID, r.PostForm.Get("csrf")) {
				h.renderError(w, r, 403, "The form has expired. Go back, reload the page and try again.")
				return
			}
		}
		next(w, r, v)
	})
}

func (h *Handler) load(ctx context.Context, w http.ResponseWriter, r *http.Request) (*viewer, error) {
	sess, err := h.Sessions.Load(ctx, w, r)
	if err != nil {
		return nil, err
	}
	u, err := h.Store.User(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	t, err := h.Store.Tenant(ctx, sess.TenantID)
	if err != nil {
		return nil, err
	}
	all, err := h.Store.TenantsForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &viewer{Session: sess, User: u, Tenant: t, Tenants: all}, nil
}

// fail logs an unexpected error and shows the generic 500 page.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("app", "path", r.URL.Path, "err", err)
	h.renderError(w, r, 500, "")
}
