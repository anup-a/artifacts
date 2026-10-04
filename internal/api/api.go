// Package api mounts the publisher API, viewer comment API and artifact serving.
package api

import (
	"context"
	"github.com/anup-a/artifacts/internal/agents"
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
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
	Runtime      []byte // comments.js bytes served at /_rt/comments.js
	// OnView is told about every page view of a current artifact index.
	OnView func(artifactID string)
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
}

type server struct {
	Deps
	adminKey *store.Key // set in self-host mode
	idem     idempotency
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
