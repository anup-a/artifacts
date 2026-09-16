package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// version stores one version with a single index.html blob for an artifact.
func (e *env) version(id string, n int, size int64) {
	e.t.Helper()
	ctx := context.Background()
	sha := strings.Repeat("a", 63) + string(rune('0'+n))
	if err := e.st.UpsertBlob(ctx, sha, size); err != nil {
		e.t.Fatal(err)
	}
	v := store.Version{ArtifactID: id, Version: n, CreatedAt: e.clock.t, Bytes: size, FileCount: 1}
	if err := e.st.AddVersion(ctx, v, []store.File{{Path: "index.html", SHA256: sha, Size: size, ContentType: "text/html"}}); err != nil {
		e.t.Fatal(err)
	}
	e.clock.t = e.clock.t.Add(time.Minute)
}

func (e *env) thread(artifactID, id, body string) {
	e.t.Helper()
	th := store.Thread{ID: id, ArtifactID: artifactID, Version: 1, AnchorJSON: `{"kind":"text","quote":"hello world"}`, Status: "open", AnchorStatus: "attached", CreatedAt: e.clock.t}
	c := store.Comment{ID: "cm_" + id, ThreadID: id, AuthorName: "Viewer One", AuthorKind: "viewer", Body: body, CreatedAt: e.clock.t}
	if err := e.st.CreateThread(context.Background(), th, c); err != nil {
		e.t.Fatal(err)
	}
}

func TestArtifactDetailVersionsAndRollback(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.version(a.ID, 1, 100)
	e.version(a.ID, 2, 250)

	res := e.get("/app/artifacts/aaaaaaaaaaaa")
	want(t, res, 200, "Quarterly report", "http://share.test/a/aaaaaaaaaaaa/", "<iframe", "Current", "/app/artifacts/aaaaaaaaaaaa/rollback/1", "Roll back", "Confirm delete", "No open comments")
	if strings.Contains(res.Body, "/rollback/2\"") {
		t.Fatal("current version offers rollback")
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-src 'self' http://share.test") {
		t.Fatalf("csp lacks frame-src: %s", res.Header.Get("Content-Security-Policy"))
	}
	want(t, e.get("/app/artifacts/nope"), 404, "does not exist")

	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/1", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa")
	got, err := e.st.Artifact(context.Background(), a.ID)
	if err != nil || got.CurrentVersion != 3 {
		t.Fatalf("after rollback: %+v %v", got, err)
	}
	vs, _ := e.st.Versions(context.Background(), a.ID)
	if len(vs) != 3 || vs[2].Bytes != 100 || vs[2].FileCount != 1 {
		t.Fatalf("versions %+v", vs)
	}
	want(t, e.get("/app/artifacts/aaaaaaaaaaaa"), 200, "v3", "3 versions kept")
	// Rolling back to the current version is a no-op redirect; unknown versions 404.
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/3", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa")
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/9", "/app/artifacts/aaaaaaaaaaaa", nil), 404, "version does not exist")
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/x", "/app/artifacts/aaaaaaaaaaaa", nil), 404)

	// Delete from the detail page's danger zone lands on the list.
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/delete", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts")
	want(t, e.get("/app/artifacts/aaaaaaaaaaaa"), 404)
}

func TestArtifactDetailComments(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.version(a.ID, 1, 100)
	e.thread(a.ID, "th_one", "Typo in the heading")
	e.thread(a.ID, "th_two", "Chart is unreadable")

	res := e.get("/app/artifacts/aaaaaaaaaaaa")
	want(t, res, 200, "2 open · 0 resolved", "hello world", "Typo in the heading", "Chart is unreadable", "Viewer One", `id="th_one"`, "/comments/th_one/reply", "/comments/th_one/resolve")
	want(t, e.get("/app/artifacts"), 200, `href="/app/artifacts/aaaaaaaaaaaa#comments"`)

	// Reply as the signed-in owner.
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/comments/th_one/reply", "/app/artifacts/aaaaaaaaaaaa", url.Values{"body": {"   "}}), 400, "Write a reply")
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/comments/th_one/reply", "/app/artifacts/aaaaaaaaaaaa", url.Values{"body": {"bad\x00control"}}), 400)
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/comments/th_one/reply", "/app/artifacts/aaaaaaaaaaaa", url.Values{"body": {"  Fixed in <b>v2</b>, thanks  "}}), "/app/artifacts/aaaaaaaaaaaa#th_one")
	cs, err := e.st.Comments(context.Background(), []string{"th_one"})
	if err != nil || len(cs["th_one"]) != 2 {
		t.Fatalf("comments %+v %v", cs, err)
	}
	if c := cs["th_one"][1]; c.AuthorKind != "owner" || c.AuthorName != "a@x.io" || c.Body != "Fixed in v2, thanks" {
		t.Fatalf("owner reply %+v", c)
	}
	res = e.get("/app/artifacts/aaaaaaaaaaaa")
	want(t, res, 200, "Fixed in v2, thanks", ">owner<")

	// Resolve moves the thread under the fold and updates the list count.
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/comments/th_one/resolve", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa#th_one")
	res = e.get("/app/artifacts/aaaaaaaaaaaa")
	want(t, res, 200, "1 open · 1 resolved", "1 resolved thread", `class="thread is-resolved" id="th_one"`)
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/comments/th_one/resolve", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa#th_one") // idempotent
	n, _ := e.st.CountOpenThreads(context.Background(), a.ID)
	if n != 1 {
		t.Fatalf("open threads %d", n)
	}

	// Threads of other artifacts and unknown ids are invisible.
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/comments/th_nope/resolve", "/app/artifacts/aaaaaaaaaaaa", nil), 404, "thread does not exist")
	b := e.publish("bbbbbbbbbbbb", "Other", "")
	want(t, e.post("/app/artifacts/"+b.ID+"/comments/th_two/reply", "/app/artifacts/aaaaaaaaaaaa", url.Values{"body": {"x"}}), 404)
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		0: "just now", 30 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago",
		26 * time.Hour: "1d ago", 12 * 24 * time.Hour: "12d ago", 45 * 24 * time.Hour: "Aug 2, 2026", -2 * time.Hour: "in 2h",
	}
	for d, s := range cases {
		if got := relativeTime(now.Add(-d), now); got != s {
			t.Errorf("ago(-%v) = %q, want %q", d, got, s)
		}
	}
	if relativeTime(time.Time{}, now) != "" {
		t.Fatal("zero time")
	}
	if q := anchorQuote(`{"kind":"element","css":"#chart"}`); q != "#chart" {
		t.Fatalf("element anchor %q", q)
	}
	if q := anchorQuote("not json"); q != "" {
		t.Fatalf("bad anchor %q", q)
	}
}
