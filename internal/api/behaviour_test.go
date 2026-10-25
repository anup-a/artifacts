package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/ratelimit"
)

// TestDeleteLetsGCReclaimBlobs covers the leak where an artifact deleted with an
// open comment thread kept its version (and therefore its blobs) forever: the
// thread protected the version from pruning and the 410 made it unresolvable.
func TestDeleteLetsGCReclaimBlobs(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	ctx := context.Background()
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "typo"), nil))
	if code != 201 {
		t.Fatalf("create thread: %d %v", code, th)
	}
	if n, _ := d.Store.CountOpenThreads(ctx, id); n != 1 {
		t.Fatalf("open threads = %d, want 1", n)
	}

	res, err := http.DefaultClient.Do(authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, tok))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}

	vs, err := d.Store.Versions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("versions after delete = %d, want 0 (open threads must be resolved first)", len(vs))
	}

	if err := gcOnce(ctx, d, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	zero, err := d.Store.ZeroRefBlobs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(zero) != 0 {
		t.Fatalf("blobs left uncollected after GC: %v", zero)
	}
	if ok, _ := d.Blobs.Exists(ctx, sha("<p>hello world</p>")); ok {
		t.Fatal("blob bytes still present after GC")
	}
}

func TestWriteOpsOnDeletedArtifactAreGone(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	res, err := http.DefaultClient.Do(authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, tok))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	code, _ := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "<p>v2</p>"}}, nil))
	if code != 410 {
		t.Fatalf("PUT on deleted artifact: got %d, want 410", code)
	}
	code, _ = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/rollback/1", tok))
	if code != 410 {
		t.Fatalf("rollback on deleted artifact: got %d, want 410", code)
	}
}

func TestPublishRateLimitReturns429(t *testing.T) {
	_, d, tok := newTestServer(t, false)
	// Same deps, but a one-publish-per-minute limiter.
	d.PublishLimiter = ratelimit.New(1)
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	limited := httptest.NewServer(h)
	t.Cleanup(limited.Close)

	code, _ := do(t, publishReq(t, "POST", limited.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>one</p>"}}, nil))
	if code != 201 {
		t.Fatalf("first publish: got %d, want 201", code)
	}
	code, b := do(t, publishReq(t, "POST", limited.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>two</p>"}}, nil))
	if code != 429 {
		t.Fatalf("second publish: got %d %v, want 429", code, b)
	}
	env, _ := b["error"].(map[string]any)
	if env["code"] != "RATE_LIMITED" {
		t.Fatalf("error code = %v, want RATE_LIMITED", env["code"])
	}
}

func TestPinnedURLOfPrunedVersionIs404(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	// VersionsKept is 3 in the test config, so publishing 4 versions prunes v1.
	id := publishOne(t, srv.URL, tok, "<p>v1</p>")
	for _, body := range []string{"<p>v2</p>", "<p>v3</p>", "<p>v4</p>"} {
		code, b := do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", body}}, nil))
		if code != 200 {
			t.Fatalf("publish %s: %d %v", body, code, b)
		}
	}
	res, err := http.Get(srv.URL + "/a/" + id + "/v/1/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("pinned URL of pruned version: got %d, want 404", res.StatusCode)
	}
	// The surviving pinned version still serves.
	res, err = http.Get(srv.URL + "/a/" + id + "/v/4/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("pinned URL of kept version: got %d, want 200", res.StatusCode)
	}
}

func TestViewerListRejectsUnknownStatus(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	code, b := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments?status=resolved", "", nil))
	if code != 400 {
		t.Fatalf("status=resolved: got %d %v, want 400", code, b)
	}
	for _, st := range []string{"", "?status=open", "?status=all"} {
		url := srv.URL + "/c/" + id + "/comments"
		if st != "" {
			url += st
		}
		code, b := do(t, jsonReq(t, "GET", url, "", nil))
		if code != 200 {
			t.Fatalf("%q: got %d %v, want 200", st, code, b)
		}
	}
}
