package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/portal"
)

func TestKeyEndpointRemoved(t *testing.T) {
	for _, managed := range []bool{true, false} {
		srv, _, _ := newTestServer(t, managed)
		res, err := http.Post(srv.URL+"/api/keys", "application/json", strings.NewReader(`{"email":"a@b.co"}`))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 404 {
			t.Fatalf("managed=%v: status %d, want 404", managed, res.StatusCode)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	res, _ := http.Get(srv.URL + "/api/artifacts")
	if res.StatusCode != 401 {
		t.Fatalf("no auth: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/artifacts", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 401 {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 200 {
		t.Fatalf("admin token: %d", res.StatusCode)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing request id")
	}
}

// App-created keys must work next to ADMIN_TOKEN, and a revoked key must not.
func TestAppKeysWorkInSelfHostMode(t *testing.T) {
	srv, d, _ := newTestServer(t, false)
	key := mintKey(t, d, "personal")
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts", key)); code != 200 {
		t.Fatalf("app key: %d", code)
	}
	k, err := d.Store.KeyByHash(context.Background(), ids.HashKey(key))
	if err != nil {
		t.Fatal(err)
	}
	if k.LastUsedAt == nil {
		t.Fatal("last_used_at should be touched by an authenticated request")
	}
	if err := d.Store.RevokeKey(context.Background(), k.TenantID, k.ID, d.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts", key)); code != 401 {
		t.Fatalf("revoked key: %d", code)
	}
}

func TestMe(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	code, body := do(t, authReq(t, "GET", srv.URL+"/api/me", tok))
	tenant, _ := body["tenant"].(map[string]any)
	key, _ := body["key"].(map[string]any)
	if code != 200 || tenant["id"] != "admin" || tenant["kind"] != "personal" || key["id"].(float64) < 1 {
		t.Fatalf("%d %v", code, body)
	}
	k := mintKey(t, d, "integrator")
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/me", k))
	tenant, _ = body["tenant"].(map[string]any)
	key, _ = body["key"].(map[string]any)
	if code != 200 || tenant["kind"] != "integrator" || key["name"] != "test" {
		t.Fatalf("%d %v", code, body)
	}
}

func TestPortalLink(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	integrator := mintKey(t, d, "integrator")
	req := publishReq(t, "POST", srv.URL+"/api/artifacts", integrator, "", []pubFile{{"index.html", "x"}}, nil)
	req.Header.Set("X-Artifact-End-User", "cust-1")
	if code, b := do(t, req); code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	code, body := do(t, authReq(t, "POST", srv.URL+"/api/end-users/cust-1/portal-link", integrator))
	url, _ := body["url"].(string)
	if code != 200 || !strings.HasPrefix(url, "http://api.test/p/") || body["expires_at"] == nil {
		t.Fatalf("%d %v", code, body)
	}
	claims, err := portal.Verify(d.Cfg.KeySecret, strings.TrimPrefix(url, "http://api.test/p/"), d.Now())
	if err != nil {
		t.Fatal(err)
	}
	if claims.ExpiresAt.Sub(d.Now()) < 6*24*time.Hour {
		t.Fatalf("expected a seven day link, got %v", claims.ExpiresAt)
	}
	eu, err := d.Store.EndUser(context.Background(), claims.TenantID, "cust-1")
	if err != nil || eu.ID != claims.EndUserID {
		t.Fatalf("claims %+v do not match end user %+v (%v)", claims, eu, err)
	}
	if code, body = do(t, authReq(t, "POST", srv.URL+"/api/end-users/nobody/portal-link", integrator)); code != 404 {
		t.Fatalf("unknown end user: %d %v", code, body)
	}
	if code, body = do(t, authReq(t, "POST", srv.URL+"/api/end-users/cust-1/portal-link", tok)); code != 403 {
		t.Fatalf("personal tenant: %d %v", code, body)
	}
}

func TestPortalLinkNeedsSigningSecret(t *testing.T) {
	_, d, _ := newTestServer(t, false)
	d.Cfg.KeySecret = ""
	srv := serverFor(t, d)
	integrator := mintKey(t, d, "integrator")
	if code, body := do(t, authReq(t, "POST", srv.URL+"/api/end-users/cust-1/portal-link", integrator)); code != 503 {
		t.Fatalf("%d %v", code, body)
	}
}
