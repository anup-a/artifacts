package api

import (
	"context"

	"fmt"
	"net/http"
	"time"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/guest"
	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/store"
)

type guestCtx struct{}

// guestInfo is set on requests made with an unclaimed guest key.
type guestInfo struct {
	TenantID  string
	ExpiresAt time.Time
}

func guestFrom(ctx context.Context) (guestInfo, bool) {
	g, ok := ctx.Value(guestCtx{}).(guestInfo)
	return g, ok
}

// guestCheck looks up the workspace behind a guest-named key. A claimed key
// belongs to a real workspace now and passes through untouched; an unclaimed
// one past its deadline is refused with a message that says what to do.
func (s *server) guestCheck(r *http.Request, k store.Key) (context.Context, *apierr.Error) {
	ctx := r.Context()
	if k.Name != guest.KeyName {
		return ctx, nil
	}
	t, err := s.Store.Tenant(ctx, k.TenantID)
	if err != nil {
		return ctx, apierr.Internal(err)
	}
	if t.Plan != guest.Plan {
		return ctx, nil
	}
	exp := guest.ExpiresAt(t.CreatedAt)
	if !s.Now().Before(exp) {
		return ctx, &apierr.Error{Code: "UNAUTHORIZED", Status: 401,
			Message: "this guest key expired 24 hours after it was made; sign in at " + s.Cfg.APIOrigin + "/app/keys for a key that lasts"}
	}
	return context.WithValue(ctx, guestCtx{}, guestInfo{TenantID: t.ID, ExpiresAt: exp}), nil
}

// createGuest answers POST /api/guest with a fresh guest key. It is the only
// publisher route without a key, so it is metered per address.
func (s *server) createGuest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !s.Cfg.GuestPublish {
		apierr.Write(w, r, apierr.NotFound("route"))
		return
	}
	if ok, retry := s.GuestLimiter.Allow("ip:" + clientIP(r, s.Cfg.TrustProxyHeaders)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	plain, hash := ids.NewKey()
	now := s.Now()
	k, err := s.Store.CreateGuest(r.Context(), hash, plain[:9], now)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := s.guestFields(guestInfo{TenantID: k.TenantID, ExpiresAt: guest.ExpiresAt(now)})
	out["token"], out["api"] = plain, s.Cfg.APIOrigin
	out["limits"] = map[string]any{"artifacts": guest.Artifacts, "version_bytes": guest.VersionBytes, "files": guest.Files}
	funnel("guest_key", "tenant", k.TenantID)
	apierr.WriteJSON(w, 201, out)
}

// guestFields is what every guest response carries: when it all goes away
// and the link that keeps it.
func (s *server) guestFields(g guestInfo) map[string]any {
	return map[string]any{
		"guest":      true,
		"expires_at": g.ExpiresAt.Format(time.RFC3339),
		"claim_url":  s.Cfg.APIOrigin + "/app/claim/" + guest.ClaimCode(s.Cfg.KeySecret, g.TenantID),
	}
}

// limitsFor narrows the publish limits for a guest key.
func (s *server) limitsFor(ctx context.Context) config.Limits {
	l := s.Cfg.Limits
	if _, ok := guestFrom(ctx); ok {
		l.ArtifactsPerTenant = min(l.ArtifactsPerTenant, guest.Artifacts)
		l.VersionBytes = min(l.VersionBytes, guest.VersionBytes)
		l.Files = min(l.Files, guest.Files)
	}
	return l
}

// guestPublishCheck refuses what a guest page cannot be: private, or
// attributed to an integrator's end user.
func guestPublishCheck(r *http.Request, m manifest, external string) *apierr.Error {
	if _, ok := guestFrom(r.Context()); !ok {
		return nil
	}
	if m.Visibility != nil && *m.Visibility == "private" {
		return apierr.Forbidden("guest pages are public; sign in for private pages")
	}
	if external != "" {
		return apierr.Forbidden("guest keys cannot attribute pages to end users")
	}
	return nil
}

// guestThreadCheck caps open threads on a guest page.
func guestThreadCheck(a store.Artifact) *apierr.Error {
	if a.ExpiresAt != nil && a.OpenComments >= guest.OpenThreads {
		return apierr.Quota(fmt.Sprintf("this temporary page already has %d open comments", guest.OpenThreads))
	}
	return nil
}
