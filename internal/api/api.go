// Package api mounts the publisher API, viewer comment API and artifact serving.
package api

import (
	"context"
	"github.com/anup-a/artifacts/internal/agents"
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/domains"
	"github.com/anup-a/artifacts/internal/proxy"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/internal/variables"
)

type Deps struct {
	Cfg            config.Config
	Store          store.Store
	Blobs          blob.Store
	Now            func() time.Time
	PublishLimiter *ratelimit.Limiter
	CommentLimiter *ratelimit.Limiter
	PortalLimiter  *ratelimit.Limiter
	// GuestLimiter meters POST /api/guest per client address. NewHandler
	// defaults it to 5 an hour.
	GuestLimiter *ratelimit.Limiter
	// ConnectLimiter meters the unauthenticated /api/connect routes per
	// client address and route. NewHandler defaults it to 60 a minute, enough
	// for a CLI polling every two seconds.
	ConnectLimiter *ratelimit.Limiter
	Runtime        []byte // comments.js bytes served at /_rt/comments.js
	// OnView is told about every HTML page of a current artifact served to
	// a reader; see serve.Handler.OnView.
	OnView func(r *http.Request, artifactID, path string)
	// Web mounts the browser app (/app, /p) on the same mux when set.
	Web func(*http.ServeMux)
	// WebDeny renders the browser page for a refused private artifact address.
	// Set it alongside Web; without it those addresses answer a bare 404.
	WebDeny func(http.ResponseWriter, *http.Request)
	// WebGate renders the page asking a viewer for an artifact's share code.
	// Without it a code-shared artifact opens only by its one-click link.
	WebGate func(http.ResponseWriter, *http.Request, serve.GateView)
	// WebGuide renders a guide as a page of the site. Without it the guides
	// are plain pages.
	WebGuide func(http.ResponseWriter, *http.Request, agents.GuideDoc)
	// Mail sends restricted pages' sign-in codes and invites. NewHandler
	// defaults it from EMAIL_PROVIDER; nil means the server sends no mail.
	Mail auth.Mailer
	// InviteLimiter meters access invites per workspace. NewHandler defaults
	// it to 100 a day.
	InviteLimiter *ratelimit.Limiter
	// AccessLimiter meters PATCH /api/artifacts/{id}/access per key.
	// NewHandler defaults it to 30 a minute.
	AccessLimiter *ratelimit.Limiter
	// Proxy forwards pages' proxy route calls. NewHandler builds the
	// production one when variables are on; tests swap in one that may
	// reach a local upstream.
	Proxy *proxy.Proxy
	// Domains runs custom domains. Nil when DOMAINS_PROVIDER is unset: the
	// domain routes answer 404 and no Host is treated as a custom domain.
	Domains *domains.Service
	// DomainLimiter meters POST /api/domains per workspace. NewHandler
	// defaults it to 20 an hour.
	DomainLimiter *ratelimit.Limiter
}

type server struct {
	Deps
	adminKey *store.Key // set in self-host mode
	idem     idempotency
	// sealer seals and opens account variables; nil while they are off.
	sealer *variables.Sealer
}

type keyCtx struct{}

func withKey(ctx context.Context, k store.Key) context.Context {
	return context.WithValue(ctx, keyCtx{}, k)
}

// KeyFrom returns the authenticated key placed by the auth middleware.
func KeyFrom(ctx context.Context) (store.Key, bool) {
	k, ok := ctx.Value(keyCtx{}).(store.Key)
	return k, ok
}
