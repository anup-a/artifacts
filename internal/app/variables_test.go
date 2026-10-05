package app

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/variables"
)

func TestVariablesPage(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.VariablesKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	e := newEnv(t, cfg, nil)
	e.signIn("v@x.io")
	want(t, e.get("/app/settings"), 200, `href="/app/settings/variables"`)
	want(t, e.get("/app/settings/variables"), 200, "No variables yet", `type="password"`)

	const secret = "sk-or-v1-dashboard-secret"
	// A refusal keeps the name and hosts but never sends the value back.
	res := e.post("/app/settings/variables", "/app/settings/variables", url.Values{"name": {"OPENROUTER_API_KEY"}, "value": {secret}, "upstreams": {"localhost"}})
	want(t, res, 400, "not a public host name", `value="OPENROUTER_API_KEY"`)
	if strings.Contains(res.Body, secret) {
		t.Fatal("the value was rendered back")
	}
	wantRedirect(t, e.post("/app/settings/variables", "/app/settings/variables",
		url.Values{"name": {"OPENROUTER_API_KEY"}, "value": {secret}, "upstreams": {"openrouter.ai, https://api.openai.com/v1"}}),
		"/app/settings/variables?saved=OPENROUTER_API_KEY")
	res = e.get("/app/settings/variables?saved=OPENROUTER_API_KEY")
	want(t, res, 200, "Saved OPENROUTER_API_KEY", "openrouter.ai", "api.openai.com")
	if strings.Contains(res.Body, secret) {
		t.Fatal("the value is shown on the page")
	}
	if strings.Contains(e.get("/app/settings/variables?saved=<b>x</b>").Body, "<b>x</b>") {
		t.Fatal("saved echoes arbitrary text")
	}
	_, tenant := e.me()
	vs, _ := e.st.Variables(context.Background(), tenant.ID)
	if len(vs) != 1 || strings.Contains(vs[0].Sealed, secret) {
		t.Fatalf("stored: %+v", vs)
	}
	sealer, _ := variables.NewSealer(cfg.VariablesKey)
	if v, err := sealer.Open(tenant.ID, "OPENROUTER_API_KEY", vs[0].Sealed); err != nil || v != secret {
		t.Fatalf("the dashboard and the API must seal alike: %v", err)
	}
	// No CSRF token, no change.
	want(t, e.do(e.cl, "POST", "/app/settings/variables/OPENROUTER_API_KEY/delete", url.Values{}), 403)
	wantRedirect(t, e.post("/app/settings/variables/OPENROUTER_API_KEY/delete", "/app/settings/variables", nil), "/app/settings/variables")
	want(t, e.get("/app/settings/variables"), 200, "No variables yet")
	want(t, e.post("/app/settings/variables/OPENROUTER_API_KEY/delete", "/app/settings/variables", nil), 404)
}

func TestVariablesPageWhenOff(t *testing.T) {
	e := fixture(t)
	e.signIn("off@x.io")
	want(t, e.get("/app/settings/variables"), 200, "Variables are off on this server")
	want(t, e.post("/app/settings/variables", "/app/settings", url.Values{"name": {"K"}, "value": {"v"}, "upstreams": {"a.example.com"}}), 503)
}
