package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/i18n"
	"github.com/anup-a/byagent/internal/store"
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
	want(t, res, 200, "Quarterly report", "http://share.test/a/aaaaaaaaaaaa/", "<iframe", "Current", "/app/artifacts/aaaaaaaaaaaa/rollback/1", "Restore", "Confirm delete", "No open comments",
		`href="http://api.test/app/view/aaaaaaaaaaaa/v/1/"`, `href="http://api.test/app/view/aaaaaaaaaaaa/v/2/"`, `action="/app/artifacts/aaaaaaaaaaaa/versions/1/delete"`, "Delete v1")
	if strings.Contains(res.Body, "/rollback/2\"") || strings.Contains(res.Body, "/versions/2/delete") {
		t.Fatal("current version offers restore or delete")
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-src 'self' http://share.test") {
		t.Fatalf("csp lacks frame-src: %s", res.Header.Get("Content-Security-Policy"))
	}
	want(t, e.get("/app/artifacts/nope"), 404, "does not exist")

	// Restore is a pointer flip: v1 is live again and no version is added.
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/1", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa#versions")
	got, err := e.st.Artifact(context.Background(), a.ID)
	if err != nil || got.CurrentVersion != 1 {
		t.Fatalf("after restore: %+v %v", got, err)
	}
	vs, _ := e.st.Versions(context.Background(), a.ID)
	if len(vs) != 2 || vs[0].RestoredAt == nil || vs[1].RestoredAt != nil {
		t.Fatalf("versions %+v", vs)
	}
	res = e.get("/app/artifacts/aaaaaaaaaaaa")
	want(t, res, 200, "v1", "2 versions kept", "restored", "/rollback/2", "/versions/2/delete")
	if strings.Contains(res.Body, "/rollback/1\"") || strings.Contains(res.Body, "/versions/1/delete") {
		t.Fatal("restored live version offers restore or delete")
	}
	// Restoring the current version is a no-op redirect; unknown versions 404.
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/1", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa#versions")
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/9", "/app/artifacts/aaaaaaaaaaaa", nil), 404, "version does not exist")
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/rollback/x", "/app/artifacts/aaaaaaaaaaaa", nil), 404)

	// Delete: never the live version, even by a forged post; an old one goes.
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/versions/1/delete", "/app/artifacts/aaaaaaaaaaaa", nil), 409, "live version cannot be deleted")
	want(t, e.post("/app/artifacts/aaaaaaaaaaaa/versions/9/delete", "/app/artifacts/aaaaaaaaaaaa", nil), 404, "version does not exist")
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/versions/2/delete", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts/aaaaaaaaaaaa#versions")
	if vs, _ := e.st.Versions(context.Background(), a.ID); len(vs) != 1 || vs[0].Version != 1 {
		t.Fatalf("after version delete: %+v", vs)
	}
	want(t, e.get("/app/artifacts/aaaaaaaaaaaa"), 200, "1 version kept")

	// Delete from the detail page's danger zone lands on the list.
	wantRedirect(t, e.post("/app/artifacts/aaaaaaaaaaaa/delete", "/app/artifacts/aaaaaaaaaaaa", nil), "/app/artifacts")
	want(t, e.get("/app/artifacts/aaaaaaaaaaaa"), 404)
}

func TestArtifactDetailShowsVersionSummaries(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("summary00001", "Report", "")
	e.version(a.ID, 1, 100)
	v := store.Version{ArtifactID: a.ID, Version: 2, CreatedAt: e.clock.t, Summary: "added the chart\nfixed <b>totals</b>"}
	if err := e.st.AddVersion(context.Background(), v, nil); err != nil {
		t.Fatal(err)
	}
	res := e.get("/app/artifacts/summary00001")
	want(t, res, 200, `<ul aria-label="What changed in v2">`, "<li>added the chart</li>", "<li>fixed &lt;b&gt;totals&lt;/b&gt;</li>")
	if strings.Contains(res.Body, "What changed in v1") {
		t.Fatal("a version without a summary got a change list")
	}
}

func TestVersionActionsRefuseOtherWorkspaces(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.version(a.ID, 1, 100)
	e.version(a.ID, 2, 250)
	detail := "/app/artifacts/" + a.ID
	// Without the CSRF token the forms are refused even for the owner.
	want(t, e.do(e.cl, "POST", detail+"/versions/1/delete", url.Values{}), 403)
	want(t, e.do(e.cl, "POST", detail+"/rollback/1", url.Values{}), 403)

	e.cl = newClient(t)
	e.signIn("other@x.io")
	want(t, e.post(detail+"/versions/1/delete", "/app/settings", nil), 404)
	want(t, e.post(detail+"/rollback/1", "/app/settings", nil), 404)
	got, _ := e.st.Artifact(context.Background(), a.ID)
	vs, _ := e.st.Versions(context.Background(), a.ID)
	if got.CurrentVersion != 2 || len(vs) != 2 {
		t.Fatalf("another workspace changed the history: current %d, %d versions", got.CurrentVersion, len(vs))
	}
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

func TestArtifactDetailCommentsSwitch(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.version(a.ID, 1, 100)
	e.thread(a.ID, "th_one", "Typo in the heading")
	detail := "/app/artifacts/aaaaaaaaaaaa"
	action := detail + "/comments-enabled"

	// On by default: the switch offers to turn it off, no note, no "off" tag.
	res := e.get(detail)
	want(t, res, 200, `action="`+action+`"`, `name="enabled" value="0"`, `class="switch is-on"`, `aria-checked="true"`, ">On<")
	if strings.Contains(res.Body, "Comments are off") {
		t.Fatal("note shown while comments are on")
	}
	res = e.get("/app/artifacts")
	want(t, res, 200)
	if strings.Contains(res.Body, `class="badge off"`) {
		t.Fatal("off tag shown while comments are on")
	}

	// Turn off: redirect back to the comments card, note appears, threads stay.
	wantRedirect(t, e.post(action, detail, url.Values{"enabled": {"0"}}), detail+"#comments")
	got, err := e.st.Artifact(context.Background(), a.ID)
	if err != nil || got.CommentsEnabled {
		t.Fatalf("after off: %+v %v", got, err)
	}
	res = e.get(detail)
	want(t, res, 200, "Comments are off for this artifact", "existing threads stay here", "Typo in the heading",
		`name="enabled" value="1"`, `aria-checked="false"`, ">Off<")
	if strings.Contains(res.Body, `class="switch is-on"`) || strings.Contains(res.Body, "No open comments") {
		t.Fatal("switch still on, or the viewers hint shown, while comments are off")
	}
	want(t, e.get("/app/artifacts"), 200, `class="badge off"`)
	// Owner actions still work on old threads.
	wantRedirect(t, e.post(detail+"/comments/th_one/reply", detail, url.Values{"body": {"Seen, thanks"}}), detail+"#th_one")
	wantRedirect(t, e.post(detail+"/comments/th_one/resolve", detail, nil), detail+"#th_one")

	// Idempotent off, then back on.
	wantRedirect(t, e.post(action, detail, url.Values{"enabled": {"0"}}), detail+"#comments")
	wantRedirect(t, e.post(action, detail, url.Values{"enabled": {"1"}}), detail+"#comments")
	if got, _ = e.st.Artifact(context.Background(), a.ID); !got.CommentsEnabled {
		t.Fatal("not re-enabled")
	}
	want(t, e.get(detail), 200, `class="switch is-on"`)

	// Bad values and foreign ids.
	want(t, e.post(action, detail, url.Values{"enabled": {"yes"}}), 400)
	want(t, e.post(action, detail, nil), 400)
	want(t, e.post("/app/artifacts/nope/comments-enabled", detail, url.Values{"enabled": {"0"}}), 404, "does not exist")
}

// The list page's side peek loads the detail as a shell-less fragment and
// posts its forms with panel=1 to get the fresh fragment back in place of a
// redirect. Without the flag every route behaves exactly as before.
func TestArtifactPanelFragment(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.version(a.ID, 1, 100)
	e.version(a.ID, 2, 250)
	e.thread(a.ID, "th_one", "Typo in the heading")
	detail := "/app/artifacts/aaaaaaaaaaaa"

	full := e.get(detail)
	want(t, full, 200, `<aside class="sidebar">`, "<html", `class="back"`)
	if strings.Contains(full.Body, `class="peek-inner"`) {
		t.Fatal("full page carries the panel wrapper")
	}
	want(t, e.get("/app/artifacts"), 200, `class="tile-cover" href="/app/artifacts/aaaaaaaaaaaa" data-peek`)

	frag := e.get(detail + "?panel=1")
	want(t, frag, 200, `class="peek-inner" data-peek-title="Quarterly report"`, `class="icon-btn peek-close" data-peek-close`, "<iframe", "Restore", "Confirm delete",
		`class="switch is-on"`, "Typo in the heading", "/comments/th_one/reply")
	for _, s := range []string{`<aside class="sidebar">`, "<html", "<head>", `class="back"`, `class="topbar"`} {
		if strings.Contains(frag.Body, s) {
			t.Fatalf("fragment contains %q", s)
		}
	}
	if !strings.Contains(frag.Header.Get("Content-Security-Policy"), "frame-src 'self' http://share.test") {
		t.Fatalf("fragment lacks csp: %s", frag.Header.Get("Content-Security-Policy"))
	}
	want(t, e.get("/app/artifacts/nope?panel=1"), 404, "does not exist")

	// Forms inside the panel get the re-rendered fragment, not a redirect.
	res := e.post(detail+"/comments-enabled", detail, url.Values{"enabled": {"0"}, "panel": {"1"}})
	want(t, res, 200, `class="peek-inner"`, `aria-checked="false"`, "Comments are off for this artifact")
	if strings.Contains(res.Body, `<aside class="sidebar">`) {
		t.Fatal("post in panel mode rendered the shell")
	}
	if got, _ := e.st.Artifact(context.Background(), a.ID); got.CommentsEnabled {
		t.Fatal("switch did not persist")
	}
	want(t, e.post(detail+"/comments/th_one/reply", detail, url.Values{"body": {"Fixed"}, "panel": {"1"}}), 200, `class="peek-inner"`, "Fixed", ">owner<")
	want(t, e.post(detail+"/comments/th_one/reply", detail, url.Values{"body": {" "}, "panel": {"1"}}), 400, `class="peek-inner"`, "Write a reply")
	want(t, e.post(detail+"/comments/th_one/resolve", detail, url.Values{"panel": {"1"}}), 200, `class="peek-inner"`, "1 resolved thread")
	want(t, e.post(detail+"/rollback/1", detail, url.Values{"panel": {"1"}}), 200, `class="peek-inner"`, "v1", "2 versions kept", "restored")
	// Without the flag the same actions still redirect.
	wantRedirect(t, e.post(detail+"/comments-enabled", detail, url.Values{"enabled": {"1"}}), detail+"#comments")
	// Delete keeps its redirect to the list even from the panel.
	wantRedirect(t, e.post(detail+"/delete", detail, url.Values{"panel": {"1"}}), "/app/artifacts")
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		0: "just now", 30 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago",
		26 * time.Hour: "1d ago", 12 * 24 * time.Hour: "12d ago", 45 * 24 * time.Hour: "Aug 2, 2026", -2 * time.Hour: "in 2h",
	}
	for d, s := range cases {
		if got := i18n.Ago("en", now.Add(-d), now); got != s {
			t.Errorf("ago(-%v) = %q, want %q", d, got, s)
		}
	}
	if i18n.Ago("en", time.Time{}, now) != "" {
		t.Fatal("zero time")
	}
	if q := anchorQuote(`{"kind":"element","css":"#chart"}`); q != "#chart" {
		t.Fatalf("element anchor %q", q)
	}
	if q := anchorQuote("not json"); q != "" {
		t.Fatalf("bad anchor %q", q)
	}
}
