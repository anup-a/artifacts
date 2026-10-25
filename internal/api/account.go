package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/portal"
	"github.com/anup-a/byagent/internal/store"
)

const portalLinkTTL = 7 * 24 * time.Hour

// me answers GET /api/me: which tenant and key the caller is using.
func (s *server) me(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	t, err := s.Store.Tenant(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := map[string]any{
		"tenant": map[string]any{"id": t.ID, "kind": t.Kind, "name": t.Name, "plan": t.Plan},
		"key":    map[string]any{"id": k.ID, "name": k.Name},
	}
	if g, ok := guestFrom(r.Context()); ok {
		for f, v := range s.guestFields(g) {
			out[f] = v
		}
	}
	apierr.WriteJSON(w, 200, out)
}

// portalLink answers POST /api/end-users/{external_id}/portal-link with a
// signed, read-only portal URL for one of the integrator's end users.
func (s *server) portalLink(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if ok, retry := s.PortalLimiter.Allow("tenant:" + k.TenantID); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	if s.Cfg.KeySecret == "" {
		apierr.Write(w, r, apierr.Unavailable("portal links need KEY_SECRET to be configured"))
		return
	}
	t, err := s.Store.Tenant(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if t.Kind != "integrator" {
		apierr.Write(w, r, apierr.Forbidden("portal links are only available to integrator workspaces"))
		return
	}
	eu, err := s.Store.EndUser(r.Context(), k.TenantID, r.PathValue("external_id"))
	if errors.Is(err, store.ErrNotFound) {
		apierr.Write(w, r, apierr.NotFound("end user"))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	expires := s.Now().Add(portalLinkTTL)
	token := portal.Sign(s.Cfg.KeySecret, k.TenantID, eu.ID, expires)
	apierr.WriteJSON(w, 200, map[string]any{"url": s.Cfg.APIOrigin + "/p/" + token, "expires_at": expires})
}
