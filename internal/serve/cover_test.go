package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/cover"
	"github.com/anup-a/byagent/internal/store"
)

func TestGeneratedCover(t *testing.T) {
	srv, st, id := fixture(t)
	ctx := context.Background()
	_ = st.UpdateTitle(ctx, id, "Hello <world>")
	res, body := get(t, srv.URL+"/a/"+id+"/_cover.svg", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	if res.Header.Get("Cache-Control") != "no-cache" || res.Header.Get("ETag") == "" || res.Header.Get("Content-Length") == "" {
		t.Fatalf("headers %v", res.Header)
	}
	if !strings.HasPrefix(body, "<svg") || !strings.Contains(body, "Hello &lt;world&gt;") {
		t.Fatalf("body %.200s", body)
	}
	a, _ := st.Artifact(ctx, id)
	card := cover.Card{ID: id, Title: "Hello <world>", Host: "share.test", PublishedAt: a.CreatedAt.UTC(), Style: cover.PickStyle(id)}
	if body != string(cover.Render(card)) {
		t.Fatalf("endpoint must render the picked style with the share host and publish date:\n%s", body)
	}
	if !strings.Contains(body, ">share.test · "+a.CreatedAt.UTC().Format("2 Jan 2006")+"</text>") {
		t.Fatalf("footer missing: %s", body)
	}
	etag := res.Header.Get("ETag")
	if res2, _ := get(t, srv.URL+"/a/"+id+"/_cover.svg", map[string]string{"If-None-Match": etag}); res2.StatusCode != 304 {
		t.Fatalf("304 expected, got %d", res2.StatusCode)
	}
	// A retitle or a style change must bust the tag.
	_ = st.UpdateTitle(ctx, id, "Renamed")
	if res3, _ := get(t, srv.URL+"/a/"+id+"/_cover.svg", nil); res3.Header.Get("ETag") == etag {
		t.Fatal("ETag unchanged after retitle")
	}
	project := "finance"
	if err := st.SetMetadata(ctx, id, &project, nil, nil, "rings"); err != nil {
		t.Fatal(err)
	}
	card.Title, card.Project, card.Style = "Renamed", "finance", "rings"
	res4, body4 := get(t, srv.URL+"/a/"+id+"/_cover.svg", nil)
	if body4 != string(cover.Render(card)) || res4.Header.Get("ETag") == etag {
		t.Fatal("cover_style and project not honoured")
	}
	// An unknown stored style falls back to the derived one instead of failing.
	_ = st.SetMetadata(ctx, id, nil, nil, nil, "bogus")
	card.Project, card.Style = "", cover.PickStyle(id)
	if _, body5 := get(t, srv.URL+"/a/"+id+"/_cover.svg", nil); body5 != string(cover.Render(card)) {
		t.Fatal("unknown style must fall back")
	}
}

func TestPublisherCoverAndRollbackFallback(t *testing.T) {
	srv, st, id := fixture(t)
	ctx := context.Background()
	bl := fixtureBlobs
	png := "\x89PNG fake bytes"
	h := sha256.Sum256([]byte(png))
	sha := hex.EncodeToString(h[:])
	_ = bl.Put(ctx, sha, bytes.NewReader([]byte(png)), int64(len(png)))
	_ = st.UpsertBlob(ctx, sha, int64(len(png)))
	index := "<html><body>three</body></html>"
	hi := sha256.Sum256([]byte(index))
	shaIndex := hex.EncodeToString(hi[:])
	_ = bl.Put(ctx, shaIndex, bytes.NewReader([]byte(index)), int64(len(index)))
	_ = st.UpsertBlob(ctx, shaIndex, int64(len(index)))
	now := time.Now().UTC()
	if err := st.AddVersion(ctx, store.Version{ArtifactID: id, Version: 3, CreatedAt: now.Add(2 * time.Second), Bytes: 1, FileCount: 2},
		[]store.File{{Path: "index.html", SHA256: shaIndex, Size: int64(len(index)), ContentType: "text/html; charset=utf-8"}, {Path: "art/cover.png", SHA256: sha, Size: int64(len(png)), ContentType: "image/png"}}); err != nil {
		t.Fatal(err)
	}
	p := "art/cover.png"
	if err := st.SetMetadata(ctx, id, nil, nil, &p, ""); err != nil {
		t.Fatal(err)
	}
	res, body := get(t, srv.URL+"/a/"+id+"/_cover.svg", nil)
	if res.StatusCode != 200 || body != png || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("publisher cover: %d %q %v", res.StatusCode, body, res.Header)
	}
	if res.Header.Get("ETag") != `"`+sha+`"` || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers %v", res.Header)
	}
	if res2, _ := get(t, srv.URL+"/a/"+id+"/_cover.svg", map[string]string{"If-None-Match": `"` + sha + `"`}); res2.StatusCode != 304 {
		t.Fatalf("304 expected, got %d", res2.StatusCode)
	}
	// "Rollback": a newer version without the cover file falls back to generated.
	v2 := "<html><body>four</body></html>"
	h4 := sha256.Sum256([]byte(v2))
	sha4 := hex.EncodeToString(h4[:])
	_ = bl.Put(ctx, sha4, bytes.NewReader([]byte(v2)), int64(len(v2)))
	_ = st.UpsertBlob(ctx, sha4, int64(len(v2)))
	if err := st.AddVersion(ctx, store.Version{ArtifactID: id, Version: 4, CreatedAt: now.Add(3 * time.Second), Bytes: 1, FileCount: 1},
		[]store.File{{Path: "index.html", SHA256: sha4, Size: int64(len(v2)), ContentType: "text/html; charset=utf-8"}}); err != nil {
		t.Fatal(err)
	}
	res, body = get(t, srv.URL+"/a/"+id+"/_cover.svg", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(body, "<svg") {
		t.Fatalf("fallback: %d %v %.100s", res.StatusCode, res.Header, body)
	}
}

func TestCoverNotFoundAndGone(t *testing.T) {
	srv, st, id := fixture(t)
	if res, _ := get(t, srv.URL+"/a/zzzzzzzzzzzz/_cover.svg", nil); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	_ = st.SoftDelete(context.Background(), id, time.Now())
	if res, _ := get(t, srv.URL+"/a/"+id+"/_cover.svg", nil); res.StatusCode != 410 {
		t.Fatal(res.StatusCode)
	}
}

func TestServedPageCarriesOpenGraphTags(t *testing.T) {
	srv, st, id := fixture(t)
	ctx := context.Background()
	_ = st.UpdateTitle(ctx, id, `Say "hi" & <bye>`)
	res, body := get(t, srv.URL+"/a/"+id+"/", nil)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	for _, want := range []string{
		`<meta property="og:title" content="Say &#34;hi&#34; &amp; &lt;bye&gt;">`,
		`<meta property="og:image" content="http://share.test/a/` + id + `/_cover.svg">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`data-version="2"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Index(body, "og:title") > strings.Index(body, "/_rt/comments.js") {
		t.Fatalf("meta must precede the runtime tag: %s", body)
	}
	if res.Header.Get("Content-Length") == "" {
		t.Fatalf("Content-Length missing: %v", res.Header)
	}
	etag := res.Header.Get("ETag")
	// Pinned pages and comments-off pages get the tags too.
	if _, pinned := get(t, srv.URL+"/a/"+id+"/v/1/", nil); !strings.Contains(pinned, "og:image") {
		t.Fatalf("pinned page lacks og tags: %s", pinned)
	}
	_ = st.SetCommentsEnabled(ctx, id, false)
	_ = st.SetStateEnabled(ctx, id, false)
	if _, off := get(t, srv.URL+"/a/"+id+"/", nil); !strings.Contains(off, "og:image") || strings.Contains(off, "<script") {
		t.Fatalf("comments-off page: %s", off)
	}
	// Assets never get tags; a retitle changes the page ETag.
	if _, css := get(t, srv.URL+"/a/"+id+"/v/1/s/a.css", nil); strings.Contains(css, "og:") {
		t.Fatalf("css got meta: %s", css)
	}
	_ = st.SetCommentsEnabled(ctx, id, true)
	_ = st.UpdateTitle(ctx, id, "Other")
	if res2, _ := get(t, srv.URL+"/a/"+id+"/", nil); res2.Header.Get("ETag") == etag {
		t.Fatal("page ETag must change with the title")
	}
}

func TestUntitledPageUsesIDAsOpenGraphTitle(t *testing.T) {
	srv, _, id := fixture(t)
	_, body := get(t, srv.URL+"/a/"+id+"/", nil)
	if !strings.Contains(body, `<meta property="og:title" content="`+id+`">`) {
		t.Fatalf("%s", body)
	}
}
