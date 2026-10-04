package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
)

// publish inserts an artifact for the signed-in tenant straight into the store.
func (e *env) publish(id, title string, endUser string) store.Artifact {
	e.t.Helper()
	ctx := context.Background()
	_, tenant := e.me()
	_, hash := ids.NewKey()
	k, err := e.st.CreateTenantKey(ctx, tenant.ID, hash, "a@x.io", "seed", "ak_seed", e.clock.t)
	if err != nil {
		e.t.Fatal(err)
	}
	a := store.Artifact{ID: id, KeyID: k.ID, TenantID: tenant.ID, Title: title, CurrentVersion: 1, CreatedAt: e.clock.t, UpdatedAt: e.clock.t}
	if endUser != "" {
		eu, err := e.st.UpsertEndUser(ctx, tenant.ID, endUser, e.clock.t)
		if err != nil {
			e.t.Fatal(err)
		}
		a.EndUserID = &eu.ID
	}
	if err := e.st.CreateArtifact(ctx, a); err != nil {
		e.t.Fatal(err)
	}
	e.clock.t = e.clock.t.Add(time.Second)
	return a
}

// daysAgo moves the clock to noon n days before the fixture's start, so an
// artifact published next lands in that day's section (or the older list).
func (e *env) daysAgo(n int) {
	e.clock.t = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC).Add(-time.Duration(n) * 24 * time.Hour)
}

func (e *env) becomeIntegrator(name string) {
	e.t.Helper()
	wantRedirect(e.t, e.post("/app/settings/integrator", "/app/settings", url.Values{"name": {name}}), "/app/artifacts")
}

func TestArtifactsListFilterAndDelete(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	e.daysAgo(10) // lands in the older list, which carries the row menu
	e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.daysAgo(0)
	e.publish("bbbbbbbbbbbb", "", "")
	res := e.get("/app/artifacts")
	want(t, res, 200, "Quarterly report", "http://share.test/a/aaaaaaaaaaaa/", "bbbbbbbbbbbb", `href="/app/artifacts/aaaaaaaaaaaa"`, "Confirm delete")
	if strings.Contains(res.Body, "window.confirm") || strings.Contains(res.Body, "data-confirm") {
		t.Fatal("native confirm dialog still used")
	}
	if strings.Contains(res.Body, ">End user</th>") {
		t.Fatal("personal tenant shows end user column")
	}
	res = e.get("/app/artifacts?q=quarter")
	want(t, res, 200, "Quarterly report")
	if strings.Contains(res.Body, "bbbbbbbbbbbb") {
		t.Fatal("search did not filter")
	}
	want(t, e.get("/app/artifacts?q=zzz"), 200, "No artifacts match")
	want(t, e.get("/app/artifacts?cursor=!!!"), 400, "cursor")

	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/delete", "/app/artifacts", nil), "/app/artifacts")
	res = e.get("/app/artifacts")
	if strings.Contains(res.Body, "Quarterly report") {
		t.Fatal("deleted artifact still listed")
	}
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/delete", "/app/artifacts", nil), 404, "does not exist")
	want(t, e.post("/app/artifacts/nope/delete", "/app/artifacts", nil), 404)

	// Another tenant's artifact is invisible to delete.
	other := fixture(t)
	other.signIn("b@x.io")
	other.publish("cccccccccccc", "Theirs", "")
	ctx := context.Background()
	a, _ := other.st.Artifact(ctx, "cccccccccccc")
	if err := e.st.CreateArtifact(ctx, store.Artifact{ID: a.ID, KeyID: a.KeyID, TenantID: "someone-else", Title: a.Title, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}); err == nil {
		want(t, e.post("/app/artifacts/cccccccccccc/delete", "/app/artifacts", nil), 404)
	}
}

func TestArtifactsPaginate(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	for i := 0; i < pageSize+1; i++ {
		e.publish(fmt.Sprintf("art%09d", i), fmt.Sprintf("Page item %d", i), "")
	}
	res := e.get("/app/artifacts")
	want(t, res, 200, "Older", fmt.Sprintf("Page item %d", pageSize))
	if strings.Contains(res.Body, "Page item 0<") {
		t.Fatal("oldest item leaked onto first page")
	}
	next := regexp.MustCompile(`href="/app/artifacts\?cursor=([^"&]+)`).FindStringSubmatch(res.Body)
	if next == nil {
		t.Fatal("no cursor link")
	}
	res = e.get("/app/artifacts?cursor=" + next[1])
	want(t, res, 200, "Page item 0<")
	if strings.Contains(res.Body, "Older") {
		t.Fatal("last page still offers Older")
	}
}

func TestKeysCreateListRevoke(t *testing.T) {
	e := newEnv(t, baseConfig(t.TempDir()), func(h *Handler) { h.KeyLimiter = ratelimit.NewWindow(2, time.Hour) })
	e.signIn("a@x.io")
	want(t, e.get("/app/keys"), 200, "No keys yet")
	want(t, e.post("/app/keys", "/app/keys", url.Values{"name": {"  "}}), 400, "1 to 64 characters")
	res := e.post("/app/keys", "/app/keys", url.Values{"name": {"laptop"}})
	want(t, res, 200, "This is the only time the key is shown", "laptop")
	key := regexp.MustCompile(`value="(ak_[0-9A-Za-z]{43})"`).FindStringSubmatch(res.Body)
	if key == nil {
		t.Fatal("plaintext key missing")
	}
	res = e.get("/app/keys")
	want(t, res, 200, key[1][:9], "never", "Revoke")
	if strings.Contains(res.Body, key[1]) {
		t.Fatal("plaintext key shown again")
	}
	if _, err := e.st.KeyByHash(context.Background(), ids.HashKey(key[1])); err != nil {
		t.Fatalf("key not usable: %v", err)
	}
	id := regexp.MustCompile(`/app/keys/(\d+)/revoke`).FindStringSubmatch(res.Body)
	wantRedirect(t, e.post("/app/keys/"+id[1]+"/revoke", "/app/keys", nil), "/app/keys")
	want(t, e.get("/app/keys"), 200, `class="revoked"`, "Revoked", `title="2026-09-16 12:00 UTC"`)
	wantRedirect(t, e.post("/app/keys/"+id[1]+"/revoke", "/app/keys", nil), "/app/keys") // idempotent
	want(t, e.post("/app/keys/abc/revoke", "/app/keys", nil), 404)
	want(t, e.post("/app/keys/999/revoke", "/app/keys", nil), 404)
	want(t, e.post("/app/keys", "/app/keys", url.Values{"name": {"second"}}), 200)
	want(t, e.post("/app/keys", "/app/keys", url.Values{"name": {"third"}}), 429, "Too many keys")
}

func TestEndUsersAndPortal(t *testing.T) {
	e := newEnv(t, baseConfig(t.TempDir()), func(h *Handler) { h.PortalLimiter = ratelimit.New(3) }) // the unknown-user POST below spends one
	e.signIn("a@x.io")
	want(t, e.get("/app/end-users"), 404, "integrator workspaces")
	want(t, e.post("/app/end-users/x/portal-link", "/app/settings", nil), 404)
	e.becomeIntegrator("Acme Inc")
	res := e.get("/app/artifacts")
	want(t, res, 200, "End users", "Acme Inc")
	want(t, e.get("/app/end-users"), 200, "No end users yet")

	e.daysAgo(10) // older list, so the End user column is on the page
	e.publish("aaaaaaaaaaaa", "For customer one", "customer/one")
	e.publish("bbbbbbbbbbbb", "For customer two", "customer/two")
	e.daysAgo(0)
	e.publish("cccccccccccc", "Nobody", "")
	res = e.get("/app/end-users")
	want(t, res, 200, "customer/one", "customer/two", "Portal link")
	res = e.get("/app/artifacts?end_user=customer/one")
	want(t, res, 200, "For customer one", `<th class="c-user">End user</th>`, "Showing artifacts of end user")
	if strings.Contains(res.Body, "For customer two") || strings.Contains(res.Body, "Nobody") {
		t.Fatal("end_user filter ignored")
	}

	want(t, e.post("/app/end-users/nobody/portal-link", "/app/end-users", nil), 404, "does not exist")
	want(t, e.get("/app/end-users"), 200, `action="/app/end-users/customer%2Fone/portal-link"`)
	res = e.post("/app/end-users/customer%2Fone/portal-link", "/app/end-users", nil)
	want(t, res, 200, "Portal link for", "valid until 2026-09-23", "http://api.test/p/", "data-copy")
	link := regexp.MustCompile(`value="http://api.test(/p/[^"]+)"`).FindStringSubmatch(res.Body)
	if link == nil {
		t.Fatal("no portal url")
	}

	// The portal needs no session and shows only that end user's artifacts.
	fresh := newClient(t)
	p := e.do(fresh, "GET", link[1], nil)
	want(t, p, 200, "Artifacts from Acme Inc", "For customer one", "http://share.test/a/aaaaaaaaaaaa/", "expires 2026-09-23")
	for _, s := range []string{"For customer two", "Nobody", "Delete", "Sign out"} {
		if strings.Contains(p.Body, s) {
			t.Fatalf("portal leaks %q", s)
		}
	}
	if p.Header.Get("Content-Security-Policy") != e.h.csp() {
		t.Fatal("portal lacks CSP")
	}
	want(t, e.do(fresh, "GET", "/p/not-a-token", nil), 404, "invalid or has expired")
	e.clock.t = e.clock.t.Add(8 * 24 * time.Hour)
	want(t, e.do(fresh, "GET", link[1], nil), 404, "invalid or has expired")

	want(t, e.post("/app/end-users/customer%2Fone/portal-link", "/app/end-users", nil), 200)
	want(t, e.post("/app/end-users/customer%2Fone/portal-link", "/app/end-users", nil), 429, "Too many portal links")
}

func TestSettingsRenameSwitchAndIntegratorRules(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	res := e.get("/app/settings")
	want(t, res, 200, "Create an integrator workspace", "<dt>email</dt><dd>a@x.io</dd>", "personal")
	if strings.Contains(res.Body, `class="switcher"`) {
		t.Fatal("switcher shown with a single tenant")
	}
	want(t, e.post("/app/settings/tenant", "/app/settings", url.Values{"name": {""}}), 400, "1 to 64 characters")
	want(t, e.post("/app/settings/tenant", "/app/settings", url.Values{"name": {strings.Repeat("x", 65)}}), 400)
	wantRedirect(t, e.post("/app/settings/tenant", "/app/settings", url.Values{"name": {"My space"}}), "/app/settings")
	want(t, e.get("/app/settings"), 200, `value="My space"`)
	want(t, e.post("/app/settings/integrator", "/app/settings", url.Values{"name": {""}}), 400)
	_, personal := e.me()

	e.becomeIntegrator("Acme")
	_, acme := e.me()
	if acme.Kind != "integrator" || acme.Name != "Acme" || acme.ID == personal.ID {
		t.Fatalf("tenant %+v", acme)
	}
	res = e.get("/app/settings")
	want(t, res, 200, `class="switcher"`, "My space", "Acme", `action="/app/switch-tenant"`)
	if strings.Contains(res.Body, "Create an integrator workspace") {
		t.Fatal("integrator form shown on integrator tenant")
	}
	want(t, e.post("/app/settings/integrator", "/app/settings", url.Values{"name": {"Again"}}), 404)

	want(t, e.post("/app/switch-tenant", "/app/settings", url.Values{"tenant_id": {"t_nope"}}), 404, "not a member")
	wantRedirect(t, e.post("/app/switch-tenant", "/app/settings", url.Values{"tenant_id": {personal.ID}}), "/app/artifacts")
	if _, cur := e.me(); cur.ID != personal.ID {
		t.Fatalf("switch failed: %+v", cur)
	}
	want(t, e.get("/app/end-users"), 404)
}

func TestUsagePage(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	want(t, e.get("/app/usage"), 200, "This month", "September 2026", "Last month", "August 2026", "Publishes", "Storage", "Page views", "Open comments")
}

func TestPageQuota(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	e.h.Cfg.Limits.ArtifactsPerTenant = 5
	for _, id := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"} {
		e.publish(id, "Page "+id[:1], "")
	}
	want(t, e.get("/app/usage"), 200, "Live pages", "3 <span class=\"quota-of\">of 5</span>", "2 left")
	if res := e.get("/app/artifacts"); strings.Contains(res.Body, "quota-callout") {
		t.Fatal("warned at 3 of 5 pages")
	}

	e.publish("dddddddddddd", "Page d", "")
	want(t, e.get("/app/artifacts"), 200, "quota-callout", "4 of 5 pages used.")
	if res := e.get("/app/artifacts?q=Page"); strings.Contains(res.Body, "quota-callout") {
		t.Fatal("warned on a filtered list")
	}

	e.publish("eeeeeeeeeeee", "Page e", "")
	want(t, e.get("/app/artifacts"), 200, "quota-callout is-full", "You have reached the limit of 5 pages.")
	want(t, e.get("/app/usage"), 200, "quota is-full", "Limit reached.")
}
