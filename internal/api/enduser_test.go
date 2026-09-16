package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/ids"
)

func serverFor(t *testing.T, d Deps) *httptest.Server {
	t.Helper()
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func publishAs(t *testing.T, srv *httptest.Server, key, endUser, body string) (int, map[string]any) {
	t.Helper()
	req := publishReq(t, "POST", srv.URL+"/api/artifacts", key, "", []pubFile{{"index.html", body}}, nil)
	if endUser != "" {
		req.Header.Set("X-Artifact-End-User", endUser)
	}
	return do(t, req)
}

func TestEndUserAttribution(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	code, body := publishAs(t, srv, tok, "alice", "a")
	if code != 201 || body["end_user"] != "alice" {
		t.Fatalf("attributed publish: %d %v", code, body)
	}
	aliceID := body["id"].(string)
	code, body = publishAs(t, srv, tok, "", "b")
	if code != 201 || body["end_user"] != nil {
		t.Fatalf("plain publish: %d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts?end_user=alice", tok))
	list := body["artifacts"].([]any)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["id"] != aliceID || list[0].(map[string]any)["end_user"] != "alice" {
		t.Fatalf("filter: %d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok))
	if list = body["artifacts"].([]any); code != 200 || len(list) != 2 {
		t.Fatalf("unfiltered: %d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+aliceID, tok))
	if code != 200 || body["end_user"] != "alice" {
		t.Fatalf("get: %d %v", code, body)
	}
}

func TestEndUserReassignmentForbidden(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, body := publishAs(t, srv, tok, "alice", "a")
	id := body["id"].(string)
	req := publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "a2"}}, nil)
	req.Header.Set("X-Artifact-End-User", "bob")
	code, body := do(t, req)
	if code != 403 || body["error"].(map[string]any)["message"] != "artifact belongs to another end user" {
		t.Fatalf("reassign: %d %v", code, body)
	}
	req = publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "a2"}}, nil)
	req.Header.Set("X-Artifact-End-User", "alice")
	if code, body = do(t, req); code != 200 || body["end_user"] != "alice" || body["version"].(float64) != 2 {
		t.Fatalf("same end user: %d %v", code, body)
	}
	req = publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "a3"}}, nil)
	if code, body = do(t, req); code != 200 || body["end_user"] != "alice" {
		t.Fatalf("no header keeps attribution: %d %v", code, body)
	}
}

func TestEndUserHeaderValidation(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	for _, bad := range []string{strings.Repeat("x", 129), "tab\there", "café"} {
		if code, body := publishAs(t, srv, tok, bad, "z"); code != 400 {
			t.Fatalf("%q: %d %v", bad, code, body)
		}
	}
	if code, _ := publishAs(t, srv, tok, strings.Repeat("y", 128), "ok"); code != 201 {
		t.Fatalf("128 chars should be accepted: %d", code)
	}
}

func TestTenantScopedOwnership(t *testing.T) {
	srv, d, _ := newTestServer(t, true)
	k1 := mintKey(t, d, "personal")
	_, body := publishAs(t, srv, k1, "", "x")
	id := body["id"].(string)
	// A second key in the same tenant manages the same artifacts.
	k, err := d.Store.KeyByHash(context.Background(), ids.HashKey(k1))
	if err != nil {
		t.Fatal(err)
	}
	other := mintKeyIn(t, d, k.TenantID)
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, other)); code != 200 {
		t.Fatalf("same tenant: %d", code)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/artifacts/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+mintKey(t, d, "personal"))
	if code, _ := do(t, req); code != 404 {
		t.Fatalf("other tenant: %d", code)
	}
}
