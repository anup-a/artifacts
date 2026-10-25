package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/anup-a/byagent/internal/domains"
	"github.com/anup-a/byagent/internal/store"
)

// registerDomains mounts the dashboard's custom domain forms. They exist
// only when the server has a domain provider.
func (h *Handler) registerDomains(mux *http.ServeMux) {
	if h.Domains == nil {
		return
	}
	mux.HandleFunc("POST /app/settings/domains", h.private(h.addDomain))
	mux.HandleFunc("POST /app/settings/domains/{domain}/check", h.private(h.checkDomain))
	mux.HandleFunc("POST /app/settings/domains/{domain}/delete", h.private(h.deleteDomain))
	mux.HandleFunc("POST /app/artifacts/{id}/domains", h.private(h.mountArtifact))
	mux.HandleFunc("POST /app/artifacts/{id}/domains/remove", h.private(h.unmountArtifact))
	mux.HandleFunc("POST /app/artifacts/{id}/primary-domain", h.private(h.setPrimaryDomain))
}

// settingsDomains is the Custom domains card on the settings page.
type settingsDomains struct {
	Target  string
	Max     int
	Domains []domains.DomainView
	Error   string
}

func (h *Handler) settingsDomains(ctx context.Context, tenantID, msg string) (*settingsDomains, error) {
	if h.Domains == nil {
		return nil, nil
	}
	ds, err := h.Store.Domains(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := &settingsDomains{Target: h.Domains.Target, Max: h.Domains.MaxDomains, Error: msg, Domains: []domains.DomainView{}}
	for _, d := range ds {
		ms, err := h.Store.Mounts(ctx, d.Hostname)
		if err != nil {
			return nil, err
		}
		out.Domains = append(out.Domains, h.Domains.View(d, ms, h.Now()))
	}
	return out, nil
}

func (h *Handler) addDomain(w http.ResponseWriter, r *http.Request, v *viewer) {
	if _, _, e := h.Domains.Add(r.Context(), v.Tenant.ID, r.PostForm.Get("domain")); e != nil {
		h.renderSettingsDomains(w, r, e.Status, v, e.Message)
		return
	}
	http.Redirect(w, r, "/app/settings#domains", http.StatusFound)
}

func (h *Handler) checkDomain(w http.ResponseWriter, r *http.Request, v *viewer) {
	d, e := h.Domains.Owned(r.Context(), v.Tenant.ID, r.PathValue("domain"))
	if e != nil {
		h.renderSettingsDomains(w, r, e.Status, v, e.Message)
		return
	}
	if !h.Cfg.ReadOnly {
		h.Domains.Refresh(r.Context(), d, false)
	}
	http.Redirect(w, r, "/app/settings#domains", http.StatusFound)
}

func (h *Handler) deleteDomain(w http.ResponseWriter, r *http.Request, v *viewer) {
	if e := h.Domains.Remove(r.Context(), v.Tenant.ID, r.PathValue("domain")); e != nil {
		h.renderSettingsDomains(w, r, e.Status, v, e.Message)
		return
	}
	http.Redirect(w, r, "/app/settings#domains", http.StatusFound)
}

// renderSettingsDomains shows the settings page with a message on the
// Custom domains card.
func (h *Handler) renderSettingsDomains(w http.ResponseWriter, r *http.Request, status int, v *viewer, msg string) {
	idents, err := h.Store.Identities(r.Context(), v.User.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sd, err := h.settingsDomains(r.Context(), v.Tenant.ID, msg)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, status, "settings", tr(r, "Settings"), v, settingsData{Identities: idents, Domains: sd})
}

// artifactDomains is the Domains card on an artifact's page: where the page
// is mounted, and the workspace's domains it could be mounted on.
type artifactDomains struct {
	Mounts  []artifactMount
	Domains []store.Domain
	Error   string
}

type artifactMount struct {
	domains.MountView
	// Key identifies the mount in a form: domain and path, space separated.
	Key    string
	Active bool
}

func (h *Handler) artifactDomains(ctx context.Context, tenantID, artifactID, msg string) (*artifactDomains, error) {
	if h.Domains == nil {
		return nil, nil
	}
	ds, err := h.Store.Domains(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, d := range ds {
		status[d.Hostname] = d.Status
	}
	ms, err := h.Store.ArtifactMounts(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	out := &artifactDomains{Domains: ds, Error: msg}
	for _, m := range ms {
		out.Mounts = append(out.Mounts, artifactMount{MountView: h.Domains.MountView(m), Key: m.Hostname + " " + domains.DisplayPath(m.Path), Active: status[m.Hostname] == "active"})
	}
	return out, nil
}

func (h *Handler) domainFailed(w http.ResponseWriter, r *http.Request, v *viewer, status int, msg string) {
	h.renderArtifactWith(w, r, status, v, r.PathValue("id"), artifactOpts{DomainError: msg})
}

func (h *Handler) mountArtifact(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That artifact does not exist."))
		return
	}
	if _, e := h.Domains.Mount(r.Context(), v.Tenant.ID, r.PostForm.Get("domain"), r.PostForm.Get("path"), a.ID); e != nil {
		h.domainFailed(w, r, v, e.Status, e.Message)
		return
	}
	h.backToArtifact(w, r, v, a.ID, "#domains")
}

func (h *Handler) unmountArtifact(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That artifact does not exist."))
		return
	}
	domain, path, _ := strings.Cut(r.PostForm.Get("mount"), " ")
	// Only this artifact's own mounts can be removed from its page.
	ms, err := h.Store.ArtifactMounts(r.Context(), a.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	found := false
	for _, m := range ms {
		if m.Hostname == domain && domains.DisplayPath(m.Path) == path {
			found = true
		}
	}
	if !found {
		h.domainFailed(w, r, v, 404, tr(r, "That mount does not exist."))
		return
	}
	if e := h.Domains.Unmount(r.Context(), v.Tenant.ID, domain, path); e != nil {
		h.domainFailed(w, r, v, e.Status, e.Message)
		return
	}
	h.backToArtifact(w, r, v, a.ID, "#domains")
}

// setPrimaryDomain takes "domain path" from the form, or "" to stop
// redirecting.
func (h *Handler) setPrimaryDomain(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That artifact does not exist."))
		return
	}
	domain, path, _ := strings.Cut(r.PostForm.Get("mount"), " ")
	var p *string
	if domain != "" {
		p = &path
	}
	if e := h.Domains.SetPrimary(r.Context(), v.Tenant.ID, a.ID, domain, p); e != nil {
		h.domainFailed(w, r, v, e.Status, e.Message)
		return
	}
	h.backToArtifact(w, r, v, a.ID, "#domains")
}
