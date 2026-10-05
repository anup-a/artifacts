package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func authReq(t *testing.T, method, url, tok string) *http.Request {
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	return req
}

func TestGetListDelete(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, b1 := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "One", []pubFile{{"index.html", "1"}}, nil))
	_, b2 := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Two", []pubFile{{"index.html", "2"}}, nil))
	id1, id2 := b1["id"].(string), b2["id"].(string)

	code, body := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id1, tok))
	if code != 200 || body["title"] != "One" || body["current_version"].(float64) != 1 || body["versions_count"].(float64) != 1 || body["open_comments"].(float64) != 0 {
		t.Fatalf("%d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts?limit=1", tok))
	list := body["artifacts"].([]any)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["id"] != id2 || body["next_cursor"] == nil {
		t.Fatalf("page1 %d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts?limit=1&cursor="+body["next_cursor"].(string), tok))
	list = body["artifacts"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != id1 {
		t.Fatalf("page2 %v", body)
	}
	code, _ = do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id1, tok))
	if code != 204 {
		t.Fatalf("delete %d", code)
	}
	code, _ = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id1, tok))
	if code != 410 {
		t.Fatalf("after delete %d", code)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok))
	if len(body["artifacts"].([]any)) != 1 {
		t.Fatalf("deleted still listed %v", body)
	}
}

func TestVersionsRollbackPrune(t *testing.T) {
	srv, d, tok := newTestServer(t, false) // VersionsKept = 3
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "v1"}}, nil))
	id := b["id"].(string)
	for _, body := range []string{"v2", "v3", "v4"} {
		if code, _ := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", body}}, nil)); code != 200 {
			t.Fatal(code)
		}
	}
	code, body := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	vs := body["versions"].([]any)
	if code != 200 || len(vs) != 3 || vs[0].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("expected versions 2,3,4 got %v", body)
	}
	// Rollback is a pointer flip: v2 is live again and no v5 is made.
	code, body = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/rollback/2", tok))
	if code != 200 || body["version"].(float64) != 2 || body["previous_version"].(float64) != 4 {
		t.Fatalf("rollback %d %v", code, body)
	}
	if fs, _ := d.Store.Files(context.Background(), id, 5); len(fs) != 0 {
		t.Fatalf("rollback copied files into a new version: %+v", fs)
	}
	if page := pageBody(t, srv.URL+"/a/"+id+"/"); !strings.HasPrefix(page, "v2") || !strings.Contains(page, `data-version="2"`) {
		t.Fatalf("live page after rollback = %q, want v2", page)
	}
	code, _ = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/rollback/1", tok))
	if code != 404 {
		t.Fatalf("rollback pruned version: %d", code)
	}
	// GC: blob v1 is zero-ref now; with olderThan in the future it should be removed
	if err := gcOnce(context.Background(), d, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Blobs.Exists(context.Background(), sha("v1")); ok {
		t.Fatal("v1 blob should be collected")
	}
	if ok, _ := d.Blobs.Exists(context.Background(), sha("v2")); !ok {
		t.Fatal("v2 blob must survive")
	}
}

func TestOtherKeyCannotSee(t *testing.T) {
	srv, d, _ := newTestServer(t, true)
	k1 := mintKey(t, d, "personal")
	k2 := mintKey(t, d, "personal")
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", k1, "", []pubFile{{"index.html", "x"}}, nil))
	id := b["id"].(string)
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, k2)); code != 404 {
		t.Fatalf("cross-key get: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, k2)); code != 404 {
		t.Fatalf("cross-key delete: %d", code)
	}
}

func TestArtifactFileReadsSourceBack(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Brief", []pubFile{{"index.html", "<h1>v1</h1>"}, {"brief.md", "# v1"}}, nil))
	id := b["id"].(string)
	if code, _ := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "Brief", []pubFile{{"index.html", "<h1>v2</h1>"}, {"brief.md", "# v2"}}, nil)); code != 200 {
		t.Fatal(code)
	}
	read := func(path, tok string) (int, string, http.Header) {
		t.Helper()
		res, err := http.DefaultClient.Do(authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/files/"+path, tok))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body), res.Header
	}
	code, body, h := read("brief.md", tok)
	if code != 200 || body != "# v2" {
		t.Fatalf("current: %d %q", code, body)
	}
	if h.Get("Content-Disposition") != "attachment" || h.Get("Content-Security-Policy") != "sandbox" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", h)
	}
	// The page comes back as published, without the comment runtime.
	if code, body, _ = read("index.html", tok); code != 200 || body != "<h1>v2</h1>" {
		t.Fatalf("index: %d %q", code, body)
	}
	if code, body, _ = read("brief.md?version=1", tok); code != 200 || body != "# v1" {
		t.Fatalf("pinned: %d %q", code, body)
	}
	if code, _, _ = read("brief.md?version=x", tok); code != 400 {
		t.Fatalf("bad version: %d", code)
	}
	if code, _, _ = read("nope.md", tok); code != 404 {
		t.Fatalf("missing file: %d", code)
	}
	if code, _, _ = read("brief.md", mintKey(t, d, "personal")); code != 404 {
		t.Fatalf("other workspace: %d", code)
	}
}
