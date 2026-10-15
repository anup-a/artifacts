package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Page state end to end on page hosts: the page's script reads and writes
// its own state at /_state, the owner reads and clears it through the API,
// publishing never touches it, and deleting the page erases it.
func TestPageStateOnPageHostAndOwnerAPI(t *testing.T) {
	srv, d, tok := pageHostServer(t, "https", nil)
	a := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>alpha</p>"))
	b := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>beta</p>"))
	hostA, hostB := a.Slug+".page.test", b.Slug+".page.test"

	w := onHost(t, srv, hostReq{method: "POST", host: hostA, path: "/_state", body: `{"items":{"vote:sam":"yes","tally":{"yes":1}}}`, hdr: fromPage(a)})
	if w.code != 200 || !strings.Contains(w.body, `"revision":1`) {
		t.Fatalf("page write: %d %s", w.code, w.body)
	}
	// The other page's host answers for its own state only, and refuses a
	// write that page A's script makes against it.
	if r := onHost(t, srv, hostReq{host: hostB, path: "/_state", hdr: fromPage(b)}); r.code != 200 || !strings.Contains(r.body, `"items":{}`) {
		t.Fatalf("page B sees A's state: %d %s", r.code, r.body)
	}
	if r := onHost(t, srv, hostReq{method: "POST", host: hostB, path: "/_state", body: `{"items":{"x":1}}`, hdr: fromPage(a)}); r.code != 403 {
		t.Fatalf("A wrote to B: %d %s", r.code, r.body)
	}
	if r := onHost(t, srv, hostReq{host: hostB, path: "/_state", hdr: fromPage(a)}); r.code != 403 {
		t.Fatalf("A read B: %d %s", r.code, r.body)
	}
	// The page host has no comment-style id route for state: another
	// page's id in the path is just a missing file.
	if r := onHost(t, srv, hostReq{host: hostA, path: "/a/" + b.ID + "/_state", hdr: fromPage(a)}); r.code == 200 {
		t.Fatalf("a page host answered another page's state: %s", r.body)
	}

	// The owner reads it through the API.
	code, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+a.ID+"/state", tok))
	items, _ := got["items"].(map[string]any)
	if code != 200 || got["revision"].(float64) != 1 || items["vote:sam"] != "yes" || got["keys"].(float64) != 2 || got["state_enabled"] != true {
		t.Fatalf("owner read: %d %v", code, got)
	}
	// Another workspace cannot.
	other := mintKey(t, d, "personal")
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+a.ID+"/state", other)); code != 404 {
		t.Fatalf("other workspace read: %d", code)
	}

	// A republish makes a version and leaves state alone.
	if code, res := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+a.ID, tok, "", []pubFile{{"index.html", "<p>alpha 2</p>"}}, nil)); code != 200 || res["version"].(float64) != 2 {
		t.Fatalf("republish: %d %v", code, res)
	}
	if _, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+a.ID+"/state", tok)); got["revision"].(float64) != 1 {
		t.Fatalf("republish moved state: %v", got)
	}
	vs, _ := d.Store.Versions(t.Context(), a.ID)
	if len(vs) != 2 {
		t.Fatalf("state writes made versions: %d", len(vs))
	}

	// Clear empties it and moves the revision on.
	code, got = do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+a.ID+"/state", tok))
	if code != 200 || got["revision"].(float64) != 2 {
		t.Fatalf("clear: %d %v", code, got)
	}
	r := onHost(t, srv, hostReq{host: hostA, path: "/_state?since=1", hdr: fromPage(a)})
	var st struct {
		Revision int64          `json:"revision"`
		Items    map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.body), &st); err != nil || st.Revision != 2 || st.Items == nil || len(st.Items) != 0 {
		t.Fatalf("after clear: %s", r.body)
	}

	// Deleting the page erases its state.
	onHost(t, srv, hostReq{method: "POST", host: hostA, path: "/_state", body: `{"items":{"k":1}}`, hdr: fromPage(a)})
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+a.ID, tok)); code != 200 && code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if s, _ := d.Store.State(t.Context(), a.ID); len(s.Items) != 0 {
		t.Fatalf("delete left state: %v", s.Items)
	}
	if r := onHost(t, srv, hostReq{host: hostA, path: "/_state", hdr: fromPage(a)}); r.code != 410 {
		t.Fatalf("deleted page state: %d", r.code)
	}
}

// Readers behind a page's gate reach its state only once they are in.
func TestPageStateFollowsPageAccess(t *testing.T) {
	srv, d, tok := pageHostServer(t, "https", nil)
	a := pageOf(t, d, publishOne(t, srv.URL, tok, "<p>gated</p>"))
	if err := d.Store.SetVisibility(t.Context(), a.ID, "private"); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.SetShareCode(t.Context(), a.ID, "481523", nil); err != nil {
		t.Fatal(err)
	}
	host := a.Slug + ".page.test"
	if r := onHost(t, srv, hostReq{host: host, path: "/_state", hdr: fromPage(a)}); r.code != 404 {
		t.Fatalf("locked: %d %s", r.code, r.body)
	}
	unlock := onHost(t, srv, hostReq{host: host, path: "/?k=481523"})
	if len(unlock.set) == 0 {
		t.Fatalf("no unlock cookie: %d %v", unlock.code, unlock.hdr)
	}
	if r := onHost(t, srv, hostReq{method: "POST", host: host, path: "/_state", body: `{"items":{"a":1}}`, hdr: fromPage(a), cookies: unlock.set}); r.code != 200 {
		t.Fatalf("unlocked write: %d %s", r.code, r.body)
	}
}

func TestPublishStateSwitchAndSummary(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	page := []pubFile{{"index.html", "<p>x</p>"}}
	code, b := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"state_enabled": false, "summary": "  first line \r\n\r\nsecond line\n"}, page))
	if code != 201 || b["state_enabled"] != false {
		t.Fatalf("publish: %d %v", code, b)
	}
	id := b["id"].(string)
	if _, g := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok)); g["state_enabled"] != false {
		t.Fatalf("get: %v", g)
	}
	// Omitted keeps it; true turns it back on.
	if code, b = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, nil, page)); code != 200 || b["state_enabled"] != false {
		t.Fatalf("republish: %d %v", code, b)
	}
	if code, b = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"state_enabled": true}, page)); code != 200 || b["state_enabled"] != true {
		t.Fatalf("on again: %d %v", code, b)
	}
	_, vs := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	list := vs["versions"].([]any)
	if s := list[0].(map[string]any)["summary"]; s != "first line\nsecond line" {
		t.Fatalf("v1 summary %q", s)
	}
	if s := list[1].(map[string]any)["summary"]; s != "" {
		t.Fatalf("v2 summary %q", s)
	}
	for name, bad := range map[string]string{"too long": strings.Repeat("x", 1001), "control": "a\x07b"} {
		if code, b := do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"summary": bad}, page)); code != 400 {
			t.Errorf("%s: %d %v", name, code, b)
		}
	}
	// 1000 characters, not bytes.
	if code, b := do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"summary": strings.Repeat("é", 1000)}, page)); code != 200 {
		t.Fatalf("1000 runes: %d %v", code, b)
	}
	// No file may take the state route.
	if code, b := do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, nil, append(page, pubFile{"_state", "x"}))); code != 400 {
		t.Fatalf("_state file: %d %v", code, b)
	}
}
