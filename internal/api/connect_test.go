package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/connect"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

func postJSON(t *testing.T, url string, body any) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func connectServer(t *testing.T) (string, Deps, store.Tenant) {
	t.Helper()
	srv, d, _ := newTestServerWith(t, true, func(d *Deps) { d.Cfg.EmailProvider = "log" })
	ten := store.Tenant{ID: "t_" + ids.NewArtifactID(), Kind: "personal", Name: "Ann's workspace", CreatedAt: d.Now()}
	if err := d.Store.CreateTenant(context.Background(), ten, ""); err != nil {
		t.Fatal(err)
	}
	return srv.URL, d, ten
}

// dashboardCode is what POST /app/connect stores: approved as it is made.
func dashboardCode(t *testing.T, d Deps, tenantID string) string {
	t.Helper()
	code, now := connect.NewCode(), d.Now()
	if err := d.Store.CreateConnect(context.Background(), store.ConnectCode{CodeSHA256: connect.Hash(code), TenantID: tenantID,
		Email: "ann@example.test", CreatedAt: now, ExpiresAt: now.Add(connect.TTL), ApprovedAt: &now}); err != nil {
		t.Fatal(err)
	}
	return code
}

func TestRedeemConnectCode(t *testing.T) {
	url, d, ten := connectServer(t)
	code := dashboardCode(t, d, ten.ID)

	if c, b := do(t, postJSON(t, url+"/api/connect", map[string]any{"code": "nope"})); c != 400 {
		t.Fatalf("malformed code: %d %v", c, b)
	}
	// Retyped in lower case without the dash still works.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	c, b := do(t, postJSON(t, url+"/api/connect", map[string]any{"code": typed, "name": "claude-code on laptop"}))
	if c != 201 || b["api"] != "http://api.test" || b["status"] != "approved" {
		t.Fatalf("redeem: %d %v", c, b)
	}
	tok, _ := b["token"].(string)
	key, _ := b["key"].(map[string]any)
	ws, _ := b["workspace"].(map[string]any)
	if !strings.HasPrefix(tok, "ak_") || key["name"] != "claude-code on laptop" || key["prefix"] != tok[:9] || ws["id"] != ten.ID || ws["name"] != ten.Name {
		t.Fatalf("redeem body: %v", b)
	}
	if c, b := do(t, authReq(t, "GET", url+"/api/me", tok)); c != 200 {
		t.Fatalf("new key does not work: %d %v", c, b)
	}
	if c, b := do(t, postJSON(t, url+"/api/connect", map[string]any{"code": code})); c != 404 || !strings.Contains(b["error"].(map[string]any)["message"].(string), "already used") {
		t.Fatalf("second redeem: %d %v", c, b)
	}
}

// The browser flow: the CLI starts, polls while nobody has approved, gets
// its key once someone signed in approves, and never again after.
func TestDeviceConnectFlow(t *testing.T) {
	url, d, ten := connectServer(t)
	c, start := do(t, postJSON(t, url+"/api/connect/device", map[string]any{"name": "codex on box"}))
	if c != 201 {
		t.Fatalf("start: %d %v", c, start)
	}
	device, _ := start["device_code"].(string)
	user, _ := start["user_code"].(string)
	if !strings.HasPrefix(device, "dc_") || connect.Normalize(user) == "" || start["verify_url"] != "http://api.test/app/connect?code="+user || start["name"] != "codex on box" {
		t.Fatalf("start body: %v", start)
	}
	poll := func() (int, map[string]any) {
		return do(t, postJSON(t, url+"/api/connect/device/token", map[string]any{"device_code": device}))
	}
	if c, b := poll(); c != 202 || b["status"] != "pending" {
		t.Fatalf("pending: %d %v", c, b)
	}
	// The short code shown on screen must not redeem the CLI's request.
	if c, _ := do(t, postJSON(t, url+"/api/connect", map[string]any{"code": user})); c != 404 {
		t.Fatalf("user code redeemed a device request: %d", c)
	}
	if err := d.Store.ApproveConnect(context.Background(), connect.Hash(user), ten.ID, "ann@example.test", d.Now()); err != nil {
		t.Fatal(err)
	}
	c, b := poll()
	tok, _ := b["token"].(string)
	if c != 201 || !strings.HasPrefix(tok, "ak_") || b["key"].(map[string]any)["name"] != "codex on box" {
		t.Fatalf("approved poll: %d %v", c, b)
	}
	if c, _ := poll(); c != 404 {
		t.Fatalf("key handed out twice: %d", c)
	}
	if c, _ := do(t, postJSON(t, url+"/api/connect/device/token", map[string]any{"device_code": "dc_forged"})); c != 404 {
		t.Fatalf("forged device code: %d", c)
	}
}

func TestDeviceConnectNeedsSignIn(t *testing.T) {
	srv, _, _ := newTestServer(t, true)
	if c, b := do(t, postJSON(t, srv.URL+"/api/connect/device", map[string]any{})); c != 503 {
		t.Fatalf("without sign-in: %d %v", c, b)
	}
}

// A version records the client header as its agent when the manifest names
// none, and the manifest still wins when it does.
func TestClientHeaderRecordsAgent(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<p>one</p>"}}
	req := publishWith(t, "POST", srv.URL+"/api/artifacts", tok, nil, files)
	req.Header.Set(ClientHeader, "codex")
	c, b := do(t, req)
	if c != 201 {
		t.Fatalf("publish: %d %v", c, b)
	}
	id := b["id"].(string)
	req = publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"agent": "claude-code"}, []pubFile{{"index.html", "<p>two</p>"}})
	req.Header.Set(ClientHeader, "codex")
	do(t, req)
	req = publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, nil, []pubFile{{"index.html", "<p>three</p>"}})
	req.Header.Set(ClientHeader, strings.Repeat("x", 65))
	do(t, req)

	_, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	vs := list["versions"].([]any)
	agents := []any{}
	for _, v := range vs {
		agents = append(agents, v.(map[string]any)["agent"])
	}
	if len(agents) != 3 || agents[0] != "codex" || agents[1] != "claude-code" || agents[2] != "" {
		t.Fatalf("agents: %v", agents)
	}
}
