package app

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

// TestMain hashes page passwords cheaply: argon2 at full cost takes seconds
// under the race detector.
func TestMain(m *testing.M) {
	serve.PasswordCost.Time, serve.PasswordCost.MemoryKiB = 1, 64
	os.Exit(m.Run())
}

// The gate page asks for what the page's door takes, escapes what a visitor
// typed, and never shows anything of the page itself.
func TestGatePageKinds(t *testing.T) {
	e := fixture(t)
	for _, c := range []struct {
		g      serve.GateView
		status int
		has    []string
	}{
		{serve.GateView{ArtifactID: "gate00000001"}, 200, []string{"Enter the code", `action="/a/gate00000001/_unlock"`}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "password"}, 200, []string{"Enter the password", `type="password"`, `action="/a/gate00000001/_unlock"`}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "password", Wrong: true}, 403, []string{"did not match"}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "password", Busy: true}, 503, []string{"Try again in a moment"}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "email"}, 200, []string{"Enter your email address", `action="/a/gate00000001/_access/email"`}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "email", Sent: true, Email: `x"><script>@a.co`}, 200, []string{`action="/a/gate00000001/_access/code"`, "&lt;script&gt;", "Send a new code"}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "email", Token: "tok123", Email: "sam@acme.com"}, 200, []string{"Continue as", `name="t" value="tok123"`, `action="/a/gate00000001/_access/link"`}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "email", Expired: true}, 403, []string{"expired or was already used"}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "email", NoMail: true}, 200, []string{"cannot send the email"}},
		{serve.GateView{ArtifactID: "gate00000001", Kind: "email", Sent: true, Email: "a@b.co", Locked: true}, 429, []string{"Too many tries"}},
	} {
		w := httptest.NewRecorder()
		e.h.GateArtifactAccess(w, httptest.NewRequest("GET", "http://share.test/a/gate00000001/", nil), c.g)
		body := w.Body.String()
		if w.Code != c.status || strings.Contains(body, "<script>@") {
			t.Fatalf("%+v: %d %s", c.g, w.Code, body)
		}
		for _, s := range c.has {
			if !strings.Contains(body, s) {
				t.Fatalf("%+v: missing %q", c.g, s)
			}
		}
	}
}

func TestDashboardAccessCard(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	a := e.publish("accessdash01", "Board deck", "")
	e.version(a.ID, 1, 100)
	detail := "/app/artifacts/" + a.ID
	action := detail + "/access"
	ctx := context.Background()

	want(t, e.get(detail), 200, `id="access"`, `action="`+action+`"`, "Anyone with the link")
	want(t, e.do(e.cl, "POST", action, url.Values{"mode": {"password"}, "password": {"longenough"}}), 403) // no CSRF

	want(t, e.post(action, detail, url.Values{"mode": {"password"}, "password": {"short"}}), 400, "8 to 128 characters")
	want(t, e.post(action, detail, url.Values{"mode": {"password"}}), 400, "Enter a password")
	wantRedirect(t, e.post(action, detail, url.Values{"mode": {"password"}, "password": {"longenough"}}), detail+"#access")
	got, _ := e.st.Artifact(ctx, a.ID)
	if got.AccessMode != serve.AccessPassword || got.Visibility != "private" || got.AccessPassword == "" || strings.Contains(got.AccessPassword, "longenough") {
		t.Fatalf("password: %+v", got)
	}
	res := e.get(detail)
	want(t, res, 200, "Anyone with the password", "Share link, password needed", "http://share.test/a/"+a.ID+"/", "leave empty to keep the current one")
	if strings.Contains(res.Body, got.AccessPassword) {
		t.Fatal("dashboard shows the password hash")
	}
	// An empty password keeps the current one.
	wantRedirect(t, e.post(action, detail, url.Values{"mode": {"password"}}), detail+"#access")
	if again, _ := e.st.Artifact(ctx, a.ID); again.AccessPassword != got.AccessPassword {
		t.Fatal("empty password field changed the password")
	}
	// No share code on a page with a password.
	want(t, e.post(detail+"/share-code", detail, url.Values{"action": {"issue"}}), 400, "already has a password")

	want(t, e.post(action, detail, url.Values{"mode": {"restricted"}, "allow": {"sam@acme.com\nnot an email@"}}), 400)
	wantRedirect(t, e.post(action, detail, url.Values{"mode": {"restricted"}, "allow": {"Sam@Acme.com\n@corp.example, lee@x.org"}}), detail+"#access")
	got, _ = e.st.Artifact(ctx, a.ID)
	if got.AccessMode != serve.AccessRestricted || strings.Join(got.AccessAllow, ",") != "@corp.example,lee@x.org,sam@acme.com" || got.AccessPassword != "" {
		t.Fatalf("restricted: %+v", got)
	}
	want(t, e.get(detail), 200, "Only people you choose", "2 addresses, 1 domain", "sam@acme.com")

	wantRedirect(t, e.post(action, detail, url.Values{"mode": {"restricted"}, "allow": {""}}), detail+"#access")
	got, _ = e.st.Artifact(ctx, a.ID)
	if got.AccessMode != "" || got.Visibility != "private" {
		t.Fatalf("empty list: %+v", got)
	}
	want(t, e.get(detail), 200, "Only your workspace")

	wantRedirect(t, e.post(action, detail, url.Values{"mode": {"anyone_with_link"}}), detail+"#access")
	if got, _ = e.st.Artifact(ctx, a.ID); got.Visibility != "public" {
		t.Fatalf("link: %+v", got)
	}
	want(t, e.post(action, detail, url.Values{"mode": {"everyone"}}), 400, "Choose who can open")

	// Someone else's page is not theirs to change.
	e.cl = newClient(t)
	e.signIn("other@x.io")
	want(t, e.post(action, "/app/settings", url.Values{"mode": {"anyone_with_link"}}), 404)
}

func TestDashboardAccessSavesAreMetered(t *testing.T) {
	e := newEnv(t, baseConfig(t.TempDir()), func(h *Handler) { h.AccessLimiter = ratelimit.New(2) })
	e.signIn("owner@x.io")
	a := e.publish("accessdash02", "Board deck", "")
	e.version(a.ID, 1, 100)
	detail := "/app/artifacts/" + a.ID
	for range 2 {
		wantRedirect(t, e.post(detail+"/access", detail, url.Values{"mode": {"password"}, "password": {"longenough"}}), detail+"#access")
	}
	want(t, e.post(detail+"/access", detail, url.Values{"mode": {"password"}, "password": {"longenough"}}), 429, "Too many changes")
}

func TestDashboardMarksVerifiedComments(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	a := e.publish("accessdash03", "Board deck", "")
	e.version(a.ID, 1, 100)
	e.thread(a.ID, "th_verified", "Typed name, not verified")
	if err := e.st.AddComment(context.Background(), store.Comment{ID: "cm_v", ThreadID: "th_verified", AuthorName: "lee@corp.example", AuthorKind: "viewer", Body: "From the gate", CreatedAt: e.clock.t, Verified: true}); err != nil {
		t.Fatal(err)
	}
	res := e.get("/app/artifacts/" + a.ID)
	want(t, res, 200, "lee@corp.example", "verified email")
	if strings.Count(res.Body, ">verified email<") != 1 {
		t.Fatal("the typed name was marked verified too")
	}
}
