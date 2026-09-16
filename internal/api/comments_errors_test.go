package api

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestOwnerCommentErrorPaths covers the owner-side validation and lookup
// failures: bad filters, unknown threads, threads of another artifact, and a
// second resolve that is a no-op.
func TestOwnerCommentErrorPaths(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	other := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	_, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", `{"author_name":"Ann","body":"typo","anchor":`+anchor+`,"version":1}`, nil))
	tid := th["id"].(string)

	for _, q := range []string{"?status=bogus", "?since=yesterday"} {
		code, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments"+q, tok))
		if code != 400 || b["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("%s: %d %v", q, code, b)
		}
	}
	code, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments?status=resolved&since=2026-01-01T00:00:00Z", tok))
	if code != 200 || len(b["threads"].([]any)) != 0 {
		t.Fatalf("resolved filter: %d %v", code, b)
	}
	code, b = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments?status=all", tok))
	if code != 200 || len(b["threads"].([]any)) != 1 {
		t.Fatalf("all filter: %d %v", code, b)
	}

	// The thread belongs to id, so the same thread under other is not found.
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+other+"/comments/"+tid+"/reply", `{"body":"x"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 404 {
		t.Fatalf("cross-artifact reply: %d", code)
	}
	code, _ = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/th_nope/resolve", tok))
	if code != 404 {
		t.Fatalf("unknown thread resolve: %d", code)
	}
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/reply", `{"body":""}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 400 {
		t.Fatalf("empty owner reply: %d", code)
	}
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/reply", `{"body":`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 400 {
		t.Fatalf("malformed owner reply: %d", code)
	}
	for i := 0; i < 2; i++ {
		code, b = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/resolve", tok))
		if code != 200 || b["status"] != "resolved" {
			t.Fatalf("resolve %d: %d %v", i, code, b)
		}
	}
}

// TestViewerCommentErrorPaths covers the anonymous list and seen endpoints.
func TestViewerCommentErrorPaths(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	_, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", `{"author_name":"Ann","body":"typo","anchor":`+anchor+`,"version":1}`, nil))
	tid := th["id"].(string)

	for _, q := range []string{"?status=resolved", "?version=-1", "?version=x"} {
		code, _ := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments"+q, "", nil))
		if code != 400 {
			t.Fatalf("%s: %d", q, code)
		}
	}
	code, b := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments?version=1", "", nil))
	if code != 200 || len(b["threads"].([]any)) != 1 {
		t.Fatalf("version filter: %d %v", code, b)
	}
	code, _ = do(t, jsonReq(t, "GET", srv.URL+"/c/nope/comments", "", nil))
	if code != 404 {
		t.Fatalf("unknown artifact list: %d", code)
	}
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/th_nope/seen", `{"version":1,"anchor_status":"attached"}`, nil))
	if code != 404 {
		t.Fatalf("unknown thread seen: %d", code)
	}
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/"+tid+"/seen", `{"version":`, nil))
	if code != 400 {
		t.Fatalf("malformed seen: %d", code)
	}
	// A stale version or unknown status is accepted but ignored.
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/"+tid+"/seen", `{"version":9,"anchor_status":"drifted"}`, nil))
	if code != 204 {
		t.Fatalf("stale seen: %d", code)
	}
	_, b = do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments", "", nil))
	if b["threads"].([]any)[0].(map[string]any)["anchor_status"] != "unknown" {
		t.Fatalf("stale seen changed status: %v", b)
	}
}

func TestHealthz(t *testing.T) {
	srv, _, _ := newTestServer(t, false)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

// TestRunGCTicksAndStops drives the GC loop with a short ticker and checks it
// returns once the context is cancelled.
func TestRunGCTicksAndStops(t *testing.T) {
	_, d, _ := newTestServer(t, false)
	if err := d.Store.UpsertBlob(context.Background(), "gcsha", 1); err != nil {
		t.Fatal(err)
	}
	d.Now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunGC(ctx, d, 10*time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		has, err := d.Store.HasBlobs(context.Background(), []string{"gcsha"})
		if err != nil {
			t.Fatal(err)
		}
		if !has["gcsha"] {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunGC did not stop")
	}
	if has, _ := d.Store.HasBlobs(context.Background(), []string{"gcsha"}); has["gcsha"] {
		t.Fatal("GC never collected the zero-ref blob")
	}
}
