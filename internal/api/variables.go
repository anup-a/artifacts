package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/proxy"
	"github.com/anup-a/byagent/internal/ratelimit"
	"github.com/anup-a/byagent/internal/serve"
	"github.com/anup-a/byagent/internal/store"
	"github.com/anup-a/byagent/internal/variables"
)

// variablesSetup turns on variables and proxy routes when the config asks
// for them, and returns the route runner for the page handler (nil: off).
func (s *server) variablesSetup() (*serve.Routes, error) {
	if !s.Cfg.VariablesEnabled() {
		return nil, nil
	}
	sealer, err := variables.NewSealer(s.Cfg.VariablesKey)
	if err != nil {
		return nil, fmt.Errorf("VARIABLES_KEY: %w", err)
	}
	s.sealer = sealer
	px := s.Proxy
	if px == nil {
		px = proxy.New(proxy.Options{})
	}
	perIP, perPage := s.Cfg.Limits.ProxyPerIPHour, s.Cfg.Limits.ProxyPerPageHour
	if perIP <= 0 {
		perIP = 100
	}
	if perPage <= 0 {
		perPage = 1000
	}
	return &serve.Routes{
		Proxy: px, Secrets: s.openSecrets,
		PerIP: ratelimit.NewWindow(perIP, time.Hour), PerPage: ratelimit.NewWindow(perPage, time.Hour),
		ClientIP: func(r *http.Request) string { return clientIP(r, s.Cfg.TrustProxyHeaders) },
		Reserved: s.reservedHosts(),
	}, nil
}

// variablesOff is the answer while VARIABLES_KEY is unset or VARIABLES=off.
func variablesOff() *apierr.Error {
	return apierr.Unavailable("account variables and proxy routes are off on this server")
}

// variablesCaller checks that the feature is on and the key belongs to a real
// workspace. Guest keys are refused: a guest page cannot use proxy routes, so
// a guest workspace has nothing to keep a secret for.
func (s *server) variablesCaller(r *http.Request) (store.Key, *apierr.Error) {
	k, _ := KeyFrom(r.Context())
	if s.sealer == nil {
		return k, variablesOff()
	}
	if _, ok := guestFrom(r.Context()); ok {
		return k, apierr.Forbidden("variables need an account; sign in at " + s.Cfg.APIOrigin + "/app/keys")
	}
	return k, nil
}

func variableJSON(v store.Variable) map[string]any {
	return map[string]any{"name": v.Name, "allowed_upstreams": v.AllowedUpstreams,
		"created_at": v.CreatedAt.UTC().Format(time.RFC3339), "updated_at": v.UpdatedAt.UTC().Format(time.RFC3339)}
}

// listVariables answers GET /api/variables: names and pinned hosts. Values
// are write-only and never leave the server.
func (s *server) listVariables(w http.ResponseWriter, r *http.Request) {
	k, e := s.variablesCaller(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	vs, err := s.Store.Variables(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, variableJSON(v))
	}
	apierr.WriteJSON(w, 200, map[string]any{"variables": out, "limit": variables.MaxPerTenant})
}

// putVariable answers PUT /api/variables/{name} with {value, allowed_upstreams}.
func (s *server) putVariable(w http.ResponseWriter, r *http.Request) {
	k, e := s.variablesCaller(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	name := r.PathValue("name")
	var body struct {
		Value            *string  `json:"value"`
		AllowedUpstreams []string `json:"allowed_upstreams"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		apierr.Write(w, r, apierr.TooLarge("body too large"))
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		apierr.Write(w, r, apierr.Invalid("body must be JSON: {\"value\": \"…\", \"allowed_upstreams\": [\"api.example.com\"]}"))
		return
	}
	v, e := s.saveVariable(r.Context(), k.TenantID, name, body.Value, body.AllowedUpstreams)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, v)
}

// saveVariable validates, seals and stores one variable. Error messages
// never include the value.
func (s *server) saveVariable(ctx context.Context, tenantID, name string, value *string, upstreams []string) (map[string]any, *apierr.Error) {
	if value == nil {
		return nil, apierr.Invalid("value is required")
	}
	hosts, err := variables.Check(name, *value, upstreams)
	if err != nil {
		return nil, apierr.Invalid(err.Error())
	}
	sealed, err := s.sealer.Seal(tenantID, name, *value)
	if err != nil {
		return nil, apierr.Internal(errors.New("sealing a variable failed"))
	}
	now := s.Now()
	v := store.Variable{TenantID: tenantID, Name: name, Sealed: sealed, AllowedUpstreams: hosts, CreatedAt: now, UpdatedAt: now}
	created, err := s.Store.PutVariable(ctx, v, variables.MaxPerTenant)
	if errors.Is(err, store.ErrVariableLimit) {
		return nil, apierr.Quota(fmt.Sprintf("a workspace holds at most %d variables; delete one first", variables.MaxPerTenant))
	}
	if err != nil {
		return nil, apierr.Internal(err)
	}
	return map[string]any{"name": name, "allowed_upstreams": hosts, "updated_at": now.UTC().Format(time.RFC3339), "created": created}, nil
}

// deleteVariable answers DELETE /api/variables/{name}.
func (s *server) deleteVariable(w http.ResponseWriter, r *http.Request) {
	k, e := s.variablesCaller(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	name := r.PathValue("name")
	err := s.Store.DeleteVariable(r.Context(), k.TenantID, name)
	if errors.Is(err, store.ErrNotFound) {
		apierr.Write(w, r, apierr.NotFound("variable"))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"name": name, "deleted": true})
}

// openSecrets is what the proxy route handler reads variables through: only
// the names the route uses are decrypted.
func (s *server) openSecrets(ctx context.Context, tenantID string, names []string) (map[string]proxy.Secret, error) {
	vs, err := s.Store.Variables(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]proxy.Secret{}
	for _, v := range vs {
		if !want[v.Name] {
			continue
		}
		val, err := s.sealer.Open(tenantID, v.Name, v.Sealed)
		out[v.Name] = proxy.Secret{Value: val, AllowedUpstreams: v.AllowedUpstreams, Err: err}
	}
	return out, nil
}

// proxyInfo is what a publish reports about the page's routes.
type proxyInfo struct {
	Routes   []string `json:"routes"`
	Warnings []string `json:"warnings"`
}

// reservedHosts are the hosts routes may not call: this server's own.
func (s *server) reservedHosts() []string {
	var out []string
	for _, o := range []string{s.Cfg.ShareOrigin, s.Cfg.APIOrigin} {
		if u, err := url.Parse(o); err == nil && u.Hostname() != "" {
			out = append(out, u.Hostname())
		}
	}
	return out
}

// checkProxyManifest validates the page's .byagent/proxy.json, if it ships
// one, before the version is stored: a broken manifest fails the publish
// with the reason rather than going live with dead routes. Variables that
// are missing or not pinned to a route's host only warn, since they can be
// set after the publish.
func (s *server) checkProxyManifest(ctx context.Context, m manifest, endUser string) (*proxyInfo, *apierr.Error) {
	var f *manifestFile
	for i := range m.Files {
		if m.Files[i].Path == proxy.ManifestPath {
			f = &m.Files[i]
		}
	}
	if f == nil {
		return nil, nil
	}
	if _, ok := guestFrom(ctx); ok {
		return nil, apierr.Forbidden("proxy routes need an account; guest pages cannot ship " + proxy.ManifestPath)
	}
	if endUser != "" && !m.ProxyRoutesForEndUser {
		return nil, apierr.Forbidden("a page published for an end user cannot ship " + proxy.ManifestPath +
			" unless the manifest sets proxy_routes_for_end_user: true, since its routes spend this workspace's variables")
	}
	if f.Size > proxy.MaxManifestBytes {
		return nil, apierr.Invalid(proxy.ManifestPath + " is larger than 64 KB")
	}
	rc, _, err := s.Blobs.Get(ctx, f.SHA256)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, proxy.MaxManifestBytes+1))
	if err != nil {
		return nil, apierr.Internal(err)
	}
	pm, err := proxy.Parse(raw, s.reservedHosts()...)
	if err != nil {
		return nil, apierr.Invalid(proxy.ManifestPath + " " + err.Error())
	}
	info := &proxyInfo{Routes: []string{}, Warnings: []string{}}
	for name := range pm.Routes {
		info.Routes = append(info.Routes, name)
	}
	sort.Strings(info.Routes)
	if s.sealer == nil {
		info.Warnings = append(info.Warnings, "proxy routes are off on this server, so these routes answer 404")
		return info, nil
	}
	k, _ := KeyFrom(ctx)
	vs, err := s.Store.Variables(ctx, k.TenantID)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	have := map[string]store.Variable{}
	for _, v := range vs {
		have[v.Name] = v
	}
	for _, name := range info.Routes {
		rt := pm.Routes[name]
		for _, vn := range rt.Vars() {
			v, ok := have[vn]
			switch {
			case !ok:
				info.Warnings = append(info.Warnings, fmt.Sprintf("route %s uses %s, which is not set: byagent vars set %s --upstream %s", name, vn, vn, rt.Upstream.Hostname()))
			case !variables.HostAllowed(rt.Upstream.Hostname(), v.AllowedUpstreams):
				info.Warnings = append(info.Warnings, fmt.Sprintf("route %s sends %s to %s, which is not in its allowed upstreams", name, vn, rt.Upstream.Hostname()))
			}
		}
	}
	return info, nil
}

// markProxyRoutes records that the version just stored carries a vetted
// manifest. Only such versions run routes.
func (s *server) markProxyRoutes(ctx context.Context, artifactID string, version int, info *proxyInfo) *apierr.Error {
	if info == nil {
		return nil
	}
	if err := s.Store.MarkProxyRoutes(ctx, artifactID, version); err != nil {
		return apierr.Internal(err)
	}
	return nil
}

// withProxy adds the page's routes to a publish response when it has any.
func withProxy(out map[string]any, info *proxyInfo) map[string]any {
	if info != nil {
		out["proxy"] = info
	}
	return out
}
