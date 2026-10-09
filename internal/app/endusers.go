package app

import (
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/portal"
	"github.com/anup-a/artifacts/internal/store"
)

// PortalLinkTTL is how long a link minted from the dashboard stays valid.
const PortalLinkTTL = 7 * 24 * time.Hour

type portalLinkView struct {
	ExternalID string
	URL        string
	ExpiresAt  time.Time
}

type endUsersData struct {
	Rows []store.EndUser
	Link *portalLinkView
}

func (h *Handler) endUsers(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.renderEndUsers(w, r, v, nil)
}

func (h *Handler) renderEndUsers(w http.ResponseWriter, r *http.Request, v *viewer, link *portalLinkView) {
	if !v.Integrator() {
		h.renderError(w, r, 404, tr(r, "End users exist only in integrator workspaces."))
		return
	}
	rows, err := h.Store.EndUsers(r.Context(), v.Tenant.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "end_users", tr(r, "End users"), v, endUsersData{Rows: rows, Link: link})
}

func (h *Handler) portalLink(w http.ResponseWriter, r *http.Request, v *viewer) {
	if !v.Integrator() {
		h.renderError(w, r, 404, tr(r, "End users exist only in integrator workspaces."))
		return
	}
	if ok, _ := h.PortalLimiter.Allow(v.Tenant.ID); !ok {
		h.renderError(w, r, 429, tr(r, "Too many portal links created. Try again in a minute."))
		return
	}
	eu, err := h.Store.EndUser(r.Context(), v.Tenant.ID, r.PathValue("external_id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That end user does not exist."))
		return
	}
	expires := h.Now().Add(PortalLinkTTL)
	token := portal.Sign(h.Cfg.KeySecret, v.Tenant.ID, eu.ID, expires)
	h.renderEndUsers(w, r, v, &portalLinkView{ExternalID: eu.ExternalID, URL: h.Cfg.APIOrigin + "/p/" + token, ExpiresAt: expires})
}
