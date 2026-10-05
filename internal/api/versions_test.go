package api

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// threeVersions publishes v1..v3 of an index.html next to a style.css that
// never changes, and returns the id.
func threeVersions(t *testing.T, srvURL, tok string, extra map[string]any) string {
	t.Helper()
	files := func(n string) []pubFile {
		return []pubFile{{"index.html", "<p>body " + n + "</p>"}, {"style.css", "p{}"}}
	}
	code, b := do(t, publishWith(t, "POST", srvURL+"/api/artifacts", tok, extra, files("1")))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	id := b["id"].(string)
	for _, n := range []string{"2", "3"} {
		if code, b := do(t, publishWith(t, "PUT", srvURL+"/api/artifacts/"+id, tok, nil, files(n))); code != 200 {
			t.Fatalf("publish v%s: %d %v", n, code, b)
		}
	}
	return id
}

func errCode(b map[string]any) any {
	e, _ := b["error"].(map[string]any)
	return e["code"]
}

func status(t *testing.T, url string) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestVersionListCarriesLiveFlagAndURLs(t *testing.T) {
	srv, _, tok := newTestServerWith(t, false, func(d *Deps) { d.Cfg.EmailProvider = "log" })
	id := threeVersions(t, srv.URL, tok, nil)

	code, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	if code != 200 || b["current_version"].(float64) != 3 || b["id"] != id {
		t.Fatalf("versions: %d %v", code, b)
	}
	vs := b["versions"].([]any)
	for i, raw := range vs {
		v := raw.(map[string]any)
		n := i + 1
		pin := "/v/" + string(rune('0'+n)) + "/"
		if v["url"] != "http://share.test/a/"+id+pin || v["preview_url"] != "http://api.test/app/view/"+id+pin {
			t.Fatalf("v%d urls: %v", n, v)
		}
		if v["current"] != (n == 3) || v["restored_at"] != nil {
			t.Fatalf("v%d flags: %v", n, v)
		}
	}

	// A private page's pinned url is the signed-in viewer too, never the
	// share origin, and the share origin refuses its old versions.
	code, b = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"visibility": "private"}, []pubFile{{"index.html", "<p>body 4</p>"}}))
	if code != 200 {
		t.Fatalf("make private: %d %v", code, b)
	}
	_, b = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	for _, raw := range b["versions"].([]any) {
		v := raw.(map[string]any)
		if !strings.HasPrefix(v["url"].(string), "http://api.test/app/view/"+id+"/v/") || v["url"] != v["preview_url"] {
			t.Fatalf("private version urls: %v", v)
		}
	}
	if code := status(t, srv.URL+"/a/"+id+"/v/1/"); code != 404 {
		t.Fatalf("anonymous read of a private page's old version: %d", code)
	}
}

func TestVersionListWithoutSignInHasNoPreview(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>x</p>")
	_, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	v := b["versions"].([]any)[0].(map[string]any)
	if v["preview_url"] != nil || v["url"] != "http://share.test/a/"+id+"/v/1/" {
		t.Fatalf("no dashboard, no preview: %v", v)
	}
}

func TestGuestVersionsHaveNoPreview(t *testing.T) {
	srv, _, _ := newTestServerWith(t, true, func(d *Deps) {
		d.Cfg.GuestPublish = true
		d.Cfg.EmailProvider = "log"
	})
	tok := newGuest(t, srv.URL)["token"].(string)
	id := publishOne(t, srv.URL, tok, "<p>guest</p>")
	_, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	v := b["versions"].([]any)[0].(map[string]any)
	if v["preview_url"] != nil {
		t.Fatalf("a guest has no dashboard to preview in: %v", v)
	}
}

func TestRestoreIsAPointerFlipThatKeepsPageSettings(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	ctx := context.Background()
	id := threeVersions(t, srv.URL, tok, map[string]any{"visibility": "private", "comments_enabled": false, "project": "Launch", "tags": []string{"plan"}})
	base := srv.URL + "/api/artifacts/" + id
	ends := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	code, sc := do(t, jsonReq(t, "POST", base+"/share-code", `{"expires_at":"`+ends.Format(time.RFC3339)+`"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 200 {
		t.Fatalf("share code: %d %v", code, sc)
	}
	if code, b := do(t, jsonReq(t, "POST", base+"/expiry", `{"expires_at":"`+ends.Format(time.RFC3339)+`"}`, map[string]string{"Authorization": "Bearer " + tok})); code != 200 {
		t.Fatalf("expiry: %d %v", code, b)
	}
	_, before := do(t, authReq(t, "GET", base, tok))
	blobsBefore, _ := d.Store.HasBlobs(ctx, []string{sha("<p>body 1</p>")})

	code, b := do(t, authReq(t, "POST", base+"/versions/1/restore", tok))
	if code != 200 || b["version"].(float64) != 1 || b["current_version"].(float64) != 1 || b["previous_version"].(float64) != 3 || b["restored_at"] == nil {
		t.Fatalf("restore: %d %v", code, b)
	}
	_, after := do(t, authReq(t, "GET", base, tok))
	for _, k := range []string{"visibility", "share_code", "share_url", "share_code_expires_at", "comments_enabled", "project", "tags", "title", "url", "versions_count"} {
		if !reflect.DeepEqual(before[k], after[k]) {
			t.Fatalf("restore changed %s: %v -> %v", k, before[k], after[k])
		}
	}
	a, _ := d.Store.Artifact(ctx, id)
	if a.CurrentVersion != 1 || a.ExpiresAt == nil || !a.ExpiresAt.Equal(ends) {
		t.Fatalf("artifact after restore: %+v", a)
	}
	// No version was added and nothing was uploaded.
	if vs, _ := d.Store.Versions(ctx, id); len(vs) != 3 || vs[0].RestoredAt == nil {
		t.Fatalf("versions after restore: %+v", vs)
	}
	if blobsAfter, _ := d.Store.HasBlobs(ctx, []string{sha("<p>body 1</p>")}); !reflect.DeepEqual(blobsBefore, blobsAfter) {
		t.Fatal("restore touched blobs")
	}

	// Again, or a version that does not exist.
	if code, b := do(t, authReq(t, "POST", base+"/versions/1/restore", tok)); code != 409 || errCode(b) != "CONFLICT" {
		t.Fatalf("restore the live version: %d %v", code, b)
	}
	if code, b := do(t, authReq(t, "POST", base+"/versions/9/restore", tok)); code != 404 || errCode(b) != "NOT_FOUND" {
		t.Fatalf("restore a missing version: %d %v", code, b)
	}
	for _, bad := range []string{"0", "-1", "x", "1.5"} {
		if code, _ := do(t, authReq(t, "POST", base+"/versions/"+bad+"/restore", tok)); code != 400 {
			t.Fatalf("restore %q: %d", bad, code)
		}
	}

	// The next publish is v4, made live, not a second v2.
	code, b = do(t, publishWith(t, "PUT", base, tok, nil, []pubFile{{"index.html", "<p>body 4</p>"}}))
	if code != 200 || b["version"].(float64) != 4 || b["visibility"] != "private" {
		t.Fatalf("publish after restore: %d %v", code, b)
	}
	_, list := do(t, authReq(t, "GET", base+"/versions", tok))
	if list["current_version"].(float64) != 4 || len(list["versions"].([]any)) != 3 {
		t.Fatalf("versions after the next publish (3 kept): %v", list)
	}
}

func TestRestoredPageServesTheOldVersion(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := threeVersions(t, srv.URL, tok, nil)
	if code, b := do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/rollback/1", tok)); code != 200 || b["version"].(float64) != 1 {
		t.Fatalf("rollback alias: %d %v", code, b)
	}
	page := pageBody(t, srv.URL+"/a/"+id+"/")
	if !strings.Contains(page, "body 1") || !strings.Contains(page, `data-version="1"`) {
		t.Fatalf("live page after restore: %s", page)
	}
	// The runtime's version switcher sees v1 as current and keeps v2 and v3.
	_, meta := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/meta", "", nil))
	if meta["current_version"].(float64) != 1 || len(meta["versions"].([]any)) != 3 {
		t.Fatalf("viewer meta after restore: %v", meta)
	}
}

func TestDeleteVersion(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	ctx := context.Background()
	id := threeVersions(t, srv.URL, tok, nil)
	base := srv.URL + "/api/artifacts/" + id
	// A reader's thread on v1, quoting text that is about to be deleted.
	if code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "this leaks a key"), nil)); code != 201 {
		t.Fatalf("thread: %d %v", code, th)
	}

	if code, b := do(t, authReq(t, "DELETE", base+"/versions/3", tok)); code != 409 || errCode(b) != "CONFLICT" {
		t.Fatalf("delete the live version: %d %v", code, b)
	}
	if code := status(t, srv.URL+"/a/"+id+"/"); code != 200 {
		t.Fatalf("live page after refused delete: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", base+"/versions/9", tok)); code != 404 {
		t.Fatalf("delete a missing version: %d", code)
	}

	code, b := do(t, authReq(t, "DELETE", base+"/versions/1", tok))
	if code != 200 || b["deleted_version"].(float64) != 1 || b["current_version"].(float64) != 3 || b["versions_count"].(float64) != 2 || b["orphaned_comments"].(float64) != 1 {
		t.Fatalf("delete v1: %d %v", code, b)
	}
	if code := status(t, srv.URL+"/a/"+id+"/v/1/"); code != 404 {
		t.Fatalf("pinned url of a deleted version: %d", code)
	}
	if code, _ := do(t, authReq(t, "GET", base+"/files/index.html?version=1", tok)); code != 404 {
		t.Fatalf("owner file read of a deleted version: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", base+"/versions/1", tok)); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
	if code, _ := do(t, authReq(t, "POST", base+"/versions/1/restore", tok)); code != 404 {
		t.Fatalf("restore a deleted version: %d", code)
	}
	// The thread survives for the owner, orphaned.
	_, cs := do(t, authReq(t, "GET", base+"/comments?status=all", tok))
	ths := cs["threads"].([]any)
	if len(ths) != 1 || ths[0].(map[string]any)["anchor_status"] != "orphaned" || ths[0].(map[string]any)["status"] != "open" {
		t.Fatalf("threads after version delete: %v", cs)
	}

	// GC erases bytes only v1 held; the stylesheet every version shares stays.
	if err := gcOnce(ctx, d, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Blobs.Exists(ctx, sha("<p>body 1</p>")); ok {
		t.Fatal("deleted version's own blob survived GC")
	}
	for _, kept := range []string{"p{}", "<p>body 2</p>", "<p>body 3</p>"} {
		if ok, _ := d.Blobs.Exists(ctx, sha(kept)); !ok {
			t.Fatalf("GC took %q, still used by a kept version", kept)
		}
	}
	if !strings.Contains(pageBody(t, srv.URL+"/a/"+id+"/v/2/"), "body 2") {
		t.Fatal("v2 stopped serving")
	}
}

// Bytes shared with another artifact survive deleting a version that used them.
func TestDeleteVersionKeepsBlobsOtherPagesUse(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	ctx := context.Background()
	id := threeVersions(t, srv.URL, tok, nil)
	other := publishOne(t, srv.URL, tok, "<p>body 1</p>")
	if code, b := do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id+"/versions/1", tok)); code != 200 {
		t.Fatalf("delete: %d %v", code, b)
	}
	if err := gcOnce(ctx, d, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pageBody(t, srv.URL+"/a/"+other+"/"), "body 1") {
		t.Fatal("deleting a version broke another page with the same bytes")
	}
}

func TestVersionOpsRefuseOtherWorkspacesAndDeletedPages(t *testing.T) {
	srv, d, _ := newTestServer(t, true)
	ctx := context.Background()
	owner, intruder := mintKey(t, d, "personal"), mintKey(t, d, "personal")
	id := threeVersions(t, srv.URL, owner, nil)
	base := srv.URL + "/api/artifacts/" + id
	for _, req := range []*http.Request{
		authReq(t, "GET", base+"/versions", intruder),
		authReq(t, "POST", base+"/versions/1/restore", intruder),
		authReq(t, "POST", base+"/rollback/1", intruder),
		authReq(t, "DELETE", base+"/versions/1", intruder),
		authReq(t, "GET", base+"/diff?from=1", intruder),
	} {
		if code, b := do(t, req); code != 404 {
			t.Fatalf("%s %s by another workspace: %d %v", req.Method, req.URL.Path, code, b)
		}
	}
	if code, _ := do(t, authReq(t, "DELETE", base+"/versions/1", "not-a-key")); code != 401 {
		t.Fatalf("delete without a valid key: %d", code)
	}
	a, _ := d.Store.Artifact(ctx, id)
	if vs, _ := d.Store.Versions(ctx, id); a.CurrentVersion != 3 || len(vs) != 3 {
		t.Fatalf("another workspace changed the history: current %d, %d versions", a.CurrentVersion, len(vs))
	}

	if code, _ := do(t, authReq(t, "DELETE", base, owner)); code != 204 {
		t.Fatalf("delete artifact: %d", code)
	}
	for _, req := range []*http.Request{
		authReq(t, "POST", base+"/versions/1/restore", owner),
		authReq(t, "DELETE", base+"/versions/1", owner),
		authReq(t, "GET", base+"/diff?from=1", owner),
	} {
		if code, _ := do(t, req); code != 410 {
			t.Fatalf("%s %s on a deleted page: %d", req.Method, req.URL.Path, code)
		}
	}
}

func TestDiffVersions(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "one"}, {"a.css", "a"}, {"old.js", "x"}}, nil))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	id := b["id"].(string)
	if code, b := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "one, longer"}, {"a.css", "a"}, {"new.png", "png"}}, nil)); code != 200 {
		t.Fatalf("publish v2: %d %v", code, b)
	}
	base := srv.URL + "/api/artifacts/" + id + "/diff"
	code, b = do(t, authReq(t, "GET", base+"?from=1", tok))
	if code != 200 || b["from"].(float64) != 1 || b["to"].(float64) != 2 || b["unchanged"].(float64) != 1 {
		t.Fatalf("diff: %d %v", code, b)
	}
	want := map[string]any{
		"added":   []any{map[string]any{"path": "new.png", "size": float64(3)}},
		"removed": []any{map[string]any{"path": "old.js", "size": float64(1)}},
		"changed": []any{map[string]any{"path": "index.html", "from_size": float64(3), "to_size": float64(11)}},
	}
	for k, w := range want {
		if !reflect.DeepEqual(b[k], w) {
			t.Fatalf("%s = %v, want %v", k, b[k], w)
		}
	}
	// Backwards, and a version against itself.
	_, back := do(t, authReq(t, "GET", base+"?from=2&to=1", tok))
	if len(back["added"].([]any)) != 1 || back["added"].([]any)[0].(map[string]any)["path"] != "old.js" {
		t.Fatalf("reverse diff: %v", back)
	}
	_, same := do(t, authReq(t, "GET", base+"?from=2&to=2", tok))
	if len(same["added"].([]any))+len(same["removed"].([]any))+len(same["changed"].([]any)) != 0 || same["unchanged"].(float64) != 3 {
		t.Fatalf("self diff: %v", same)
	}
	for q, want := range map[string]int{"": 400, "?from=x": 400, "?from=0": 400, "?from=1&to=x": 400, "?from=9": 404, "?from=1&to=9": 404} {
		if code, b := do(t, authReq(t, "GET", base+q, tok)); code != want {
			t.Fatalf("diff%s: %d %v, want %d", q, code, b, want)
		}
	}
}
