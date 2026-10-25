package app

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/connect"
	"github.com/anup-a/byagent/internal/store"
)

var promptCode = regexp.MustCompile(`login --code ([A-Z0-9]{4}-[A-Z0-9]{4})`)

// The dashboard hands out a prompt holding a one-time code that is already
// approved for this workspace, and never a key.
func TestConnectPrompt(t *testing.T) {
	e := fixture(t)
	e.signIn("ann@example.test")
	res := e.get("/app/connect")
	want(t, res, 200, "Connect your agent", `href="/app/connect" class="active"`, "npx -y byagent@latest login --code ", "/skill.md", "byagent@latest init --global", "Copy prompt")
	if strings.Contains(res.Body, "ak_") {
		t.Fatal("the prompt page shows a key")
	}
	m := promptCode.FindStringSubmatch(res.Body)
	if m == nil {
		t.Fatalf("no code in prompt:\n%s", res.Body)
	}
	_, tenant := e.me()
	c, err := e.st.ConnectByCode(context.Background(), connect.Hash(m[1]), e.clock.now())
	if err != nil || c.TenantID != tenant.ID || c.ApprovedAt == nil || c.DeviceSHA256 != "" || c.Email != "ann@example.test" {
		t.Fatalf("stored code: %+v %v", c, err)
	}
	// A server that is not the default origin says where to log in.
	if !strings.Contains(res.Body, "--api "+e.h.Cfg.APIOrigin) {
		t.Fatalf("self-hosted prompt lacks --api:\n%s", res.Body)
	}
}

// startDevice stores what POST /api/connect/device would: a request waiting
// for someone to approve it.
func startDevice(t *testing.T, e *env) string {
	t.Helper()
	code, now := connect.NewCode(), e.clock.now()
	if err := e.st.CreateConnect(context.Background(), store.ConnectCode{CodeSHA256: connect.Hash(code), DeviceSHA256: "dev-" + code,
		Name: "codex on box", CreatedAt: now, ExpiresAt: now.Add(connect.TTL)}); err != nil {
		t.Fatal(err)
	}
	return code
}

func TestApproveDeviceThroughSignIn(t *testing.T) {
	e := fixture(t)
	code := startDevice(t, e)
	link := "/app/connect?code=" + code

	// Signed out: the approval link survives the trip through sign-in.
	wantRedirect(t, e.get(link), "/app/login")
	e.signIn("ann@example.test")
	wantRedirect(t, e.get("/app/"), link)
	want(t, e.get(link), 200, "Approve an agent", "codex on box", code)

	res := e.post("/app/connect/approve", link, url.Values{"code": {code}})
	want(t, res, 200, "Connected.", "codex on box can publish to")
	_, tenant := e.me()
	k, err := e.st.RedeemConnect(context.Background(), "dev-"+code, true, "kh", "ak_x", "", e.clock.now())
	if err != nil || k.TenantID != tenant.ID || k.Name != "codex on box" {
		t.Fatalf("approved key: %+v %v", k, err)
	}
	// Approved and spent: the link now does nothing.
	want(t, e.get(link), 410, "expired or was already used")
}

func TestApproveRefusesDashboardCodesAndExpired(t *testing.T) {
	e := fixture(t)
	e.signIn("ann@example.test")
	dash := promptCode.FindStringSubmatch(e.get("/app/connect").Body)[1]
	// A dashboard code is already approved; it is not a CLI request.
	want(t, e.get("/app/connect?code="+dash), 410)
	want(t, e.get("/app/connect?code=nope"), 410, "not valid")

	code := startDevice(t, e)
	e.clock.t = e.clock.t.Add(connect.TTL + time.Minute)
	want(t, e.get("/app/connect?code="+code), 410, "expired")
}

func TestClaimLeadsToConnect(t *testing.T) {
	e := fixture(t)
	_, claim := guestWithPage(t, e)
	e.signIn("ann@example.test")
	res := e.post(claim, claim, url.Values{})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/app/connect?claimed=1" {
		t.Fatalf("claim: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	want(t, e.get("/app/connect?claimed=1"), 200, "Your guest pages are in", "login --code ")
}
