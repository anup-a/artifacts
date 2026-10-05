package api

import (
	"encoding/json"
	"net/http"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/domains"
	"github.com/anup-a/artifacts/internal/store"
)

// registerDomains mounts the custom domain API. Every route exists on every
// server; one without DOMAINS_PROVIDER answers 404 with a message saying so.
func (s *server) registerDomains(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/domains", s.withAuth(s.domainsOn(s.listDomains)))
	mux.HandleFunc("POST /api/domains", s.withAuth(s.domainsOn(s.addDomain)))
	mux.HandleFunc("GET /api/domains/{domain}", s.withAuth(s.domainsOn(s.getDomain)))
	mux.HandleFunc("DELETE /api/domains/{domain}", s.withAuth(s.domainsOn(s.deleteDomain)))
	mux.HandleFunc("GET /api/domains/{domain}/mounts", s.withAuth(s.domainsOn(s.listMounts)))
	mux.HandleFunc("POST /api/domains/{domain}/mounts", s.withAuth(s.domainsOn(s.putMount)))
	mux.HandleFunc("DELETE /api/domains/{domain}/mounts", s.withAuth(s.domainsOn(s.deleteMount)))
	mux.HandleFunc("GET /api/artifacts/{id}/primary-domain", s.withAuth(s.domainsOn(s.getPrimaryDomain)))
	mux.HandleFunc("POST /api/artifacts/{id}/primary-domain", s.withAuth(s.domainsOn(s.setPrimaryDomain)))
}

// domainsOn refuses when the server has no domain provider, and refuses
// guest keys: a custom domain is an account feature, and a guest workspace
// disappears in a day.
func (s *server) domainsOn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Domains == nil {
			apierr.Write(w, r, &apierr.Error{Code: "NOT_FOUND", Status: http.StatusNotFound, Message: "custom domains are not turned on on this server"})
			return
		}
		if _, ok := guestFrom(r.Context()); ok {
			apierr.Write(w, r, apierr.Forbidden("custom domains need an account; open the claim_url to keep this guest workspace, then use a dashboard key"))
			return
		}
		next(w, r)
	}
}

func (s *server) domainView(r *http.Request, d store.Domain) (domains.DomainView, error) {
	ms, err := s.Store.Mounts(r.Context(), d.Hostname)
	if err != nil {
		return domains.DomainView{}, err
	}
	return s.Domains.View(d, ms, s.Now()), nil
}

func (s *server) listDomains(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	ds, err := s.Store.Domains(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]domains.DomainView, 0, len(ds))
	for _, d := range ds {
		v, err := s.domainView(r, d)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		out = append(out, v)
	}
	apierr.WriteJSON(w, 200, map[string]any{"domains": out})
}

// addDomain answers POST /api/domains {"domain": "docs.example.com"}: 201
// with the DNS records to add, or 200 with the existing domain when this
// workspace already connected it.
func (s *server) addDomain(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	var in struct {
		Domain string `json:"domain"`
	}
	if e := decodeJSON(w, r, 1024, &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if ok, retry := s.DomainLimiter.Allow("tenant:" + k.TenantID); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	d, created, e := s.Domains.Add(r.Context(), k.TenantID, in.Domain)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	v, err := s.domainView(r, d)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	status := 200
	if created {
		status = 201
	}
	apierr.WriteJSON(w, status, v)
}

// getDomain answers GET /api/domains/{domain} and checks it on the way when
// it is not yet active, so polling this is how an agent waits for it.
func (s *server) getDomain(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	d, e := s.Domains.Owned(r.Context(), k.TenantID, r.PathValue("domain"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if d.Status != "active" && !s.Cfg.ReadOnly {
		d = s.Domains.Refresh(r.Context(), d, false)
	}
	v, err := s.domainView(r, d)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, v)
}

func (s *server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if e := s.Domains.Remove(r.Context(), k.TenantID, r.PathValue("domain")); e != nil {
		apierr.Write(w, r, e)
		return
	}
	w.WriteHeader(204)
}

func (s *server) listMounts(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	d, e := s.Domains.Owned(r.Context(), k.TenantID, r.PathValue("domain"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	ms, err := s.Store.Mounts(r.Context(), d.Hostname)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]domains.MountView, 0, len(ms))
	for _, m := range ms {
		out = append(out, s.Domains.MountView(m))
	}
	apierr.WriteJSON(w, 200, map[string]any{"domain": d.Hostname, "mounts": out})
}

// putMount answers POST /api/domains/{domain}/mounts {"path": "/docs",
// "artifact_id": "..."}: it serves the page there, replacing whatever was
// mounted at that path. An empty path is the root of the domain.
func (s *server) putMount(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	var in struct {
		Path       string `json:"path"`
		ArtifactID string `json:"artifact_id"`
	}
	if e := decodeJSON(w, r, 1024, &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if in.ArtifactID == "" {
		apierr.Write(w, r, apierr.Invalid("artifact_id is required"))
		return
	}
	m, e := s.Domains.Mount(r.Context(), k.TenantID, r.PathValue("domain"), in.Path, in.ArtifactID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, s.Domains.MountView(m))
}

// deleteMount answers DELETE /api/domains/{domain}/mounts?path=/docs; no
// path is the root.
func (s *server) deleteMount(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if e := s.Domains.Unmount(r.Context(), k.TenantID, r.PathValue("domain"), r.URL.Query().Get("path")); e != nil {
		apierr.Write(w, r, e)
		return
	}
	w.WriteHeader(204)
}

// primaryView is an artifact's primary domain and the mounts that could be.
func (s *server) primaryView(r *http.Request, a store.Artifact) (map[string]any, error) {
	ms, err := s.Store.ArtifactMounts(r.Context(), a.ID)
	if err != nil {
		return nil, err
	}
	var primary any
	eligible := []domains.MountView{}
	for _, m := range ms {
		v := s.Domains.MountView(m)
		if m.Primary {
			primary = v
		}
		eligible = append(eligible, v)
	}
	return map[string]any{"id": a.ID, "primary": primary, "primary_url": nilIfEmpty(s.Domains.PrimaryURL(r.Context(), a.ID)), "eligible": eligible}, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *server) getPrimaryDomain(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	v, err := s.primaryView(r, a)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, v)
}

// setPrimaryDomain answers POST /api/artifacts/{id}/primary-domain with
// {"domain": "docs.example.com", "path": "/guide"} to make that mount the
// page's primary address, or {"domain": null} to stop redirecting.
func (s *server) setPrimaryDomain(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var in struct {
		Domain *string          `json:"domain"`
		Path   *json.RawMessage `json:"path"`
	}
	if e := decodeJSON(w, r, 1024, &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	domain := ""
	if in.Domain != nil {
		domain = *in.Domain
	}
	var path *string
	if in.Path != nil && string(*in.Path) != "null" {
		var p string
		if err := json.Unmarshal(*in.Path, &p); err != nil {
			apierr.Write(w, r, apierr.Invalid("path must be a string"))
			return
		}
		path = &p
	}
	if e := s.Domains.SetPrimary(r.Context(), k.TenantID, a.ID, domain, path); e != nil {
		apierr.Write(w, r, e)
		return
	}
	v, err := s.primaryView(r, a)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, v)
}

// artifactDomains adds an artifact's custom-domain addresses to its JSON.
func (s *server) artifactDomains(r *http.Request, a store.Artifact, v map[string]any) error {
	if s.Domains == nil {
		return nil
	}
	ms, err := s.Store.ArtifactMounts(r.Context(), a.ID)
	if err != nil {
		return err
	}
	out := make([]domains.MountView, 0, len(ms))
	for _, m := range ms {
		out = append(out, s.Domains.MountView(m))
	}
	v["domains"] = out
	v["primary_url"] = nilIfEmpty(s.Domains.PrimaryURL(r.Context(), a.ID))
	return nil
}
