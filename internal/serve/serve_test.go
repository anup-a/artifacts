package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/store"
)

func fixture(t *testing.T) (*httptest.Server, store.Store, string) {
	t.Helper()
	cfg := config.Config{DB: "sqlite", Storage: "local", DataDir: t.TempDir()}
	ctx := context.Background()
	st, _ := store.Open(ctx, cfg)
	_ = st.Migrate(ctx)
	bl, _ := blob.Open(cfg)
	k, _ := st.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	id := "serve0000001"
	_ = st.CreateArtifact(ctx, store.Artifact{ID: id, KeyID: k.ID, CreatedAt: now, UpdatedAt: now})
	put := func(body string) string {
		h := sha256.Sum256([]byte(body))
		sha := hex.EncodeToString(h[:])
		_ = bl.Put(ctx, sha, bytes.NewReader([]byte(body)), int64(len(body)))
		_ = st.UpsertBlob(ctx, sha, int64(len(body)))
		return sha
	}
	v1 := put("<html><body>one</body></html>")
	css := put("body{color:red}")
	_ = st.AddVersion(ctx, store.Version{ArtifactID: id, Version: 1, CreatedAt: now, Bytes: 1, FileCount: 2},
		[]store.File{{Path: "index.html", SHA256: v1, Size: 29, ContentType: "text/html; charset=utf-8"}, {Path: "s/a.css", SHA256: css, Size: 15, ContentType: "text/css; charset=utf-8"}})
	v2 := put("<html><body>two</body></html>")
	_ = st.AddVersion(ctx, store.Version{ArtifactID: id, Version: 2, CreatedAt: now.Add(time.Second), Bytes: 1, FileCount: 1},
		[]store.File{{Path: "index.html", SHA256: v2, Size: 29, ContentType: "text/html; charset=utf-8"}})
	mux := http.NewServeMux()
	h := &Handler{Store: st, Blobs: bl, Runtime: []byte("console.log(1)"), ShareOrigin: "http://share.test"}
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st, id
}

func get(t *testing.T, url string, hdr map[string]string) (*http.Response, string) {
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func TestServeCurrentAndPinned(t *testing.T) {
	srv, _, id := fixture(t)
	res, body := get(t, srv.URL+"/a/"+id+"/", nil)
	if res.StatusCode != 200 || !strings.Contains(body, "two") || !strings.Contains(body, `data-version="2"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("ETag") == "" {
		t.Fatalf("headers %v", res.Header)
	}
	if res.Header.Get("Cache-Control") != "public, max-age=60" {
		t.Fatal(res.Header.Get("Cache-Control"))
	}
	res, body = get(t, srv.URL+"/a/"+id+"/v/1/", nil)
	if res.StatusCode != 200 || !strings.Contains(body, "one") || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("%d %s %v", res.StatusCode, body, res.Header)
	}
	res, body = get(t, srv.URL+"/a/"+id+"/v/1/s/a.css", nil)
	if res.StatusCode != 200 || body != "body{color:red}" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/css") || strings.Contains(body, "<script") {
		t.Fatalf("css %d %s", res.StatusCode, body)
	}
}

func TestServeNotFoundGoneEtag(t *testing.T) {
	srv, st, id := fixture(t)
	if res, _ := get(t, srv.URL+"/a/"+id+"/nope.html", nil); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := get(t, srv.URL+"/a/zzzzzzzzzzzz/", nil); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := get(t, srv.URL+"/a/"+id+"/v/9/", nil); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	res, _ := get(t, srv.URL+"/a/"+id+"/", nil)
	res2, _ := get(t, srv.URL+"/a/"+id+"/", map[string]string{"If-None-Match": res.Header.Get("ETag")})
	if res2.StatusCode != 304 {
		t.Fatalf("etag: %d", res2.StatusCode)
	}
	_ = st.SoftDelete(context.Background(), id, time.Now())
	if res, _ := get(t, srv.URL+"/a/"+id+"/", nil); res.StatusCode != 410 {
		t.Fatal(res.StatusCode)
	}
	res, body := get(t, srv.URL+"/_rt/comments.js", nil)
	if res.StatusCode != 200 || body != "console.log(1)" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("runtime %d %s", res.StatusCode, body)
	}
}

func TestErrorResponsesCarrySecurityHeaders(t *testing.T) {
	srv, st, id := fixture(t)

	// Test 404 for missing file
	res, _ := get(t, srv.URL+"/a/"+id+"/nope.html", nil)
	if res.StatusCode != 404 || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing file 404: status=%d, csp=%s, nosniff=%s", res.StatusCode, res.Header.Get("Content-Security-Policy"), res.Header.Get("X-Content-Type-Options"))
	}

	// Test 404 for missing artifact
	res, _ = get(t, srv.URL+"/a/zzzzzzzzzzzz/", nil)
	if res.StatusCode != 404 || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing artifact 404: status=%d, csp=%s, nosniff=%s", res.StatusCode, res.Header.Get("Content-Security-Policy"), res.Header.Get("X-Content-Type-Options"))
	}

	// Test 404 for missing version
	res, _ = get(t, srv.URL+"/a/"+id+"/v/9/", nil)
	if res.StatusCode != 404 || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing version 404: status=%d, csp=%s, nosniff=%s", res.StatusCode, res.Header.Get("Content-Security-Policy"), res.Header.Get("X-Content-Type-Options"))
	}

	// Test 410 after SoftDelete
	_ = st.SoftDelete(context.Background(), id, time.Now())
	res, _ = get(t, srv.URL+"/a/"+id+"/", nil)
	if res.StatusCode != 410 || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("deleted artifact 410: status=%d, csp=%s, nosniff=%s", res.StatusCode, res.Header.Get("Content-Security-Policy"), res.Header.Get("X-Content-Type-Options"))
	}
}

func TestRuntimeEtag304(t *testing.T) {
	srv, _, _ := fixture(t)

	// First request should return 200 with ETag and Cache-Control
	res, _ := get(t, srv.URL+"/_rt/comments.js", nil)
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	etag := res.Header.Get("ETag")
	cacheControl := res.Header.Get("Cache-Control")
	if etag == "" || cacheControl == "" {
		t.Fatalf("missing ETag or Cache-Control: etag=%s, cache=%s", etag, cacheControl)
	}
	if cacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("wrong cache control: %s", cacheControl)
	}

	// Second request with If-None-Match should return 304 with same ETag and Cache-Control
	res2, _ := get(t, srv.URL+"/_rt/comments.js", map[string]string{"If-None-Match": etag})
	if res2.StatusCode != 304 {
		t.Fatalf("expected 304, got %d", res2.StatusCode)
	}
	if res2.Header.Get("ETag") != etag {
		t.Fatalf("304 ETag mismatch: got %s, expected %s", res2.Header.Get("ETag"), etag)
	}
	if res2.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("304 Cache-Control missing or wrong: %s", res2.Header.Get("Cache-Control"))
	}
}

func TestServeSkill(t *testing.T) {
	srv, _, _ := fixture(t)
	res, body := get(t, srv.URL+"/skill.md", nil)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("content type %s", ct)
	}
	// The skill is only useful if it still teaches the publish and comment loop.
	for _, want := range []string{"artifacts publish", "artifacts comments", "artifacts resolve"} {
		if !strings.Contains(body, want) {
			t.Fatalf("skill is missing %q", want)
		}
	}
}
