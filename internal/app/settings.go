package app

import (
	"net/http"

	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/store"
)

type settingsData struct {
	Identities []store.Identity
	Error      string
	// Domains is the Custom domains card, nil when the server has none.
	Domains *settingsDomains
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.renderSettings(w, r, 200, v, "")
}

func (h *Handler) renderSettings(w http.ResponseWriter, r *http.Request, status int, v *viewer, msg string) {
	idents, err := h.Store.Identities(r.Context(), v.User.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sd, err := h.settingsDomains(r.Context(), v.Tenant.ID, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, status, "settings", tr(r, "Settings"), v, settingsData{Identities: idents, Error: msg, Domains: sd})
}

func (h *Handler) renameTenant(w http.ResponseWriter, r *http.Request, v *viewer) {
	name, ok := cleanName(r.PostForm.Get("name"))
	if !ok {
		h.renderSettings(w, r, 400, v, tr(r, "The workspace name must be 1 to 64 characters."))
		return
	}
	if err := h.Store.RenameTenant(r.Context(), v.Tenant.ID, name); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/settings", http.StatusFound)
}

func (h *Handler) createIntegrator(w http.ResponseWriter, r *http.Request, v *viewer) {
	if v.Integrator() {
		h.renderError(w, r, 404, tr(r, "Integrator workspaces are created from a personal workspace."))
		return
	}
	name, ok := cleanName(r.PostForm.Get("name"))
	if !ok {
		h.renderSettings(w, r, 400, v, tr(r, "The workspace name must be 1 to 64 characters."))
		return
	}
	t := store.Tenant{ID: "t_" + ids.NewArtifactID(), Kind: "integrator", Name: name, CreatedAt: h.Now()}
	if err := h.Store.CreateTenant(r.Context(), t, v.User.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Store.SwitchTenant(r.Context(), v.Session.ID, v.User.ID, t.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/artifacts", http.StatusFound)
}
