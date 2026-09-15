package api

import (
	"context"
	"net/http"
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
	code, body = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/rollback/2", tok))
	if code != 200 || body["version"].(float64) != 5 {
		t.Fatalf("rollback %d %v", code, body)
	}
	fs, _ := d.Store.Files(context.Background(), id, 5)
	if len(fs) != 1 || fs[0].SHA256 != sha("v2") {
		t.Fatalf("v5 should equal v2 files: %+v", fs)
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
	k1 := mintKey(t, srv.URL)
	k2 := mintKey(t, srv.URL)
	_ = d
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", k1, "", []pubFile{{"index.html", "x"}}, nil))
	id := b["id"].(string)
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, k2)); code != 404 {
		t.Fatalf("cross-key get: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, k2)); code != 404 {
		t.Fatalf("cross-key delete: %d", code)
	}
}

func mintKey(t *testing.T, base string) string {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/keys", stringsReader(`{"email":"k@x.io"}`))
	req.Header.Set("Content-Type", "application/json")
	code, body := do(t, req)
	if code != 201 {
		t.Fatalf("mint key: %d %v", code, body)
	}
	return body["key"].(string)
}
