package app

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/domains"
)

type appTXT map[string]string

func (m appTXT) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := m[name]; ok {
		return []string{v}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func domainEnv(t *testing.T) (*env, appTXT) {
	t.Helper()
	txt := appTXT{}
	e := newEnv(t, baseConfig(t.TempDir()), func(h *Handler) {
		h.Domains = &domains.Service{Store: h.Store, Provider: domains.None{}, Resolver: txt, Now: h.Now,
			Target: "domains.share.example", Scheme: "https", Own: []string{"share.test", "api.test"},
			MaxDomains: 3, Index: &domains.Index{Store: h.Store}}
	})
	return e, txt
}

func TestSettingsDomains(t *testing.T) {
	e, txt := domainEnv(t)
	e.signIn("owner@x.io")
	want(t, e.get("/app/settings"), 200, `id="domains"`, "No custom domains yet", "domains.share.example")

	wantRedirect(t, e.post("/app/settings/domains", "/app/settings", url.Values{"domain": {"Docs.Example.com"}}), "/app/settings#domains")
	res := e.get("/app/settings")
	want(t, res, 200, "docs.example.com", "Pending", "_byagent.docs", "byagent-verify=", "CNAME", "Check now")
	want(t, e.post("/app/settings/domains", "/app/settings", url.Values{"domain": {"app.share.test"}}), 400)
	want(t, e.post("/app/settings/domains", "/app/settings", url.Values{"domain": {"not a host"}}), 400)
	// The form needs its CSRF token.
	want(t, e.do(e.cl, "POST", "/app/settings/domains", url.Values{"domain": {"x.example.com"}}), 403)

	d, err := e.st.Domain(context.Background(), "docs.example.com")
	if err != nil {
		t.Fatal(err)
	}
	txt[domains.TXTName(d.Hostname)] = domains.TXTValue(d.Token)
	wantRedirect(t, e.post("/app/settings/domains/docs.example.com/check", "/app/settings", nil), "/app/settings#domains")
	want(t, e.get("/app/settings"), 200, "Active")

	// Another workspace cannot touch it.
	owner := e.cl
	e.cl = newClient(t)
	e.signIn("other@x.io")
	want(t, e.post("/app/settings/domains/docs.example.com/delete", "/app/settings", nil), 404)
	want(t, e.post("/app/settings/domains", "/app/settings", url.Values{"domain": {"docs.example.com"}}), 409, "another byagent workspace")
	e.cl = owner

	wantRedirect(t, e.post("/app/settings/domains/docs.example.com/delete", "/app/settings", nil), "/app/settings#domains")
	if _, err := e.st.Domain(context.Background(), "docs.example.com"); err == nil {
		t.Fatal("domain survived delete")
	}
}

func TestArtifactDomainsCard(t *testing.T) {
	e, txt := domainEnv(t)
	e.signIn("owner@x.io")
	a := e.publish("aaaaaaaaaaaa", "Guide", "")
	detail := "/app/artifacts/" + a.ID
	want(t, e.get(detail), 200, `id="domains"`, "No custom domains in this workspace")

	wantRedirect(t, e.post("/app/settings/domains", "/app/settings", url.Values{"domain": {"docs.example.com"}}), "/app/settings#domains")
	want(t, e.get(detail), 200, `<option value="docs.example.com">docs.example.com (pending)</option>`)
	want(t, e.post(detail+"/domains", detail, url.Values{"domain": {"docs.example.com"}, "path": {"/_x"}}), 400, "cannot start with _")
	wantRedirect(t, e.post(detail+"/domains", detail, url.Values{"domain": {"docs.example.com"}, "path": {"/guide"}}), detail+"#domains")
	res := e.get(detail)
	want(t, res, 200, "https://docs.example.com/guide/", "Domain pending")
	if strings.Contains(res.Body, "Make primary") {
		t.Fatal("a pending domain is offered as primary")
	}
	// Primary needs the domain active.
	want(t, e.post(detail+"/primary-domain", detail, url.Values{"mount": {"docs.example.com /guide"}}), 409, "only an active domain")

	d, _ := e.st.Domain(context.Background(), "docs.example.com")
	txt[domains.TXTName(d.Hostname)] = domains.TXTValue(d.Token)
	e.h.Domains.Refresh(context.Background(), d, true)
	want(t, e.get(detail), 200, "Make primary")
	wantRedirect(t, e.post(detail+"/primary-domain", detail, url.Values{"mount": {"docs.example.com /guide"}}), detail+"#domains")
	want(t, e.get(detail), 200, "Primary", "Stop redirecting")
	wantRedirect(t, e.post(detail+"/primary-domain", detail, url.Values{"mount": {""}}), detail+"#domains")

	// Only this page's own mounts can be removed from its page.
	b := e.publish("bbbbbbbbbbbb", "Other", "")
	wantRedirect(t, e.post("/app/artifacts/"+b.ID+"/domains", "/app/artifacts/"+b.ID, url.Values{"domain": {"docs.example.com"}, "path": {"/other"}}), "/app/artifacts/"+b.ID+"#domains")
	want(t, e.post(detail+"/domains/remove", detail, url.Values{"mount": {"docs.example.com /other"}}), 404)
	wantRedirect(t, e.post(detail+"/domains/remove", detail, url.Values{"mount": {"docs.example.com /guide"}}), detail+"#domains")
	if ms, _ := e.st.ArtifactMounts(context.Background(), a.ID); len(ms) != 0 {
		t.Fatalf("mount survived: %+v", ms)
	}
}

func TestDomainsHiddenWhenOff(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	a := e.publish("aaaaaaaaaaaa", "Guide", "")
	if res := e.get("/app/settings"); strings.Contains(res.Body, `id="domains"`) {
		t.Fatal("settings shows domains on a server without them")
	}
	if res := e.get("/app/artifacts/" + a.ID); strings.Contains(res.Body, `id="domains"`) {
		t.Fatal("artifact page shows domains on a server without them")
	}
	want(t, e.post("/app/settings/domains", "/app/settings", url.Values{"domain": {"docs.example.com"}}), 404)
}
