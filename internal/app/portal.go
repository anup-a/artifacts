package app

import (
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/portal"
	"github.com/anup-a/artifacts/internal/store"
)

type portalData struct {
	Tenant    store.Tenant
	Rows      []artifactRow
	ExpiresAt time.Time
}

// portal is the end-user page: a signed token, no session, read-only.
func (h *Handler) portal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := portal.Verify(h.Cfg.KeySecret, r.PathValue("token"), h.Now())
	if err != nil {
		h.renderError(w, r, 404, "This link is invalid or has expired. Ask for a new one.")
		return
	}
	t, err := h.Store.Tenant(ctx, c.TenantID)
	if err != nil {
		h.storeErr(w, r, err, "This link is invalid or has expired. Ask for a new one.")
		return
	}
	eu, err := h.Store.EndUserByID(ctx, c.TenantID, c.EndUserID)
	if err != nil {
		h.storeErr(w, r, err, "This link is invalid or has expired. Ask for a new one.")
		return
	}
	list, err := h.Store.ListArtifacts(ctx, t.ID, store.ArtifactFilter{EndUserID: eu.ID, Limit: 200})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := portalData{Tenant: t, ExpiresAt: c.ExpiresAt}
	for _, a := range list {
		d.Rows = append(d.Rows, artifactRow{Artifact: a, URL: h.shareURL(a.ID)})
	}
	h.render(w, r, 200, "portal", "Artifacts from "+t.Name, nil, d)
}
