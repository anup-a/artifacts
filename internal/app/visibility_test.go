package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/portal"
)

func TestArtifactVisibilityActionPermissionsAndPreservation(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	a := e.publish("aaaaaaaaaaaa", "Workspace draft", "")
	e.version(a.ID, 1, 100)
	e.version(a.ID, 2, 250)
	e.thread(a.ID, "th_private", "Keep this discussion")
	detail := "/app/artifacts/" + a.ID
	action := detail + "/visibility"
	ctx := context.Background()
	before, err := e.st.Artifact(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}

	want(t, e.get(detail), 200, `action="`+action+`"`, `name="visibility"`, `value="public" selected`, ">Public</span>")
	wantRedirect(t, e.do(newClient(t), "POST", action, url.Values{"visibility": {"private"}}), "/app/login")
	want(t, e.do(e.cl, "POST", action, url.Values{"visibility": {"private"}}), 403)
	want(t, e.do(e.cl, "POST", action, url.Values{"visibility": {"private"}, "csrf": {"invalid"}}), 403)
	for _, invalid := range []string{"", "PRIVATE", "unlisted"} {
		want(t, e.post(action, detail, url.Values{"visibility": {invalid}}), 400, "Choose Public or Private")
	}
	want(t, e.post("/app/artifacts/nope/visibility", detail, url.Values{"visibility": {"private"}}), 404)

	owner := e.cl
	e.cl = newClient(t)
	e.signIn("other@x.io")
	want(t, e.get(detail), 404)
	want(t, e.post(action, "/app/settings", url.Values{"visibility": {"private"}}), 404)
	e.cl = owner
	stillPublic, _ := e.st.Artifact(ctx, a.ID)
	if stillPublic.Visibility != "public" {
		t.Fatal("rejected action changed visibility")
	}

	wantRedirect(t, e.post(action, detail, url.Values{"visibility": {"private"}}), detail+"#visibility")
	got, err := e.st.Artifact(ctx, a.ID)
	if err != nil || got.Visibility != "private" || got.Title != before.Title || got.CurrentVersion != before.CurrentVersion || got.CommentsEnabled != before.CommentsEnabled || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("visibility change altered content or failed: %+v %v", got, err)
	}
	res := e.get(detail)
	want(t, res, 200, ">Private</span>", "Private link", "signed-in members of the owning workspace", "Viewer comments are unavailable while this artifact is private", "Keep this discussion", `src="http://api.test/app/view/aaaaaaaaaaaa/"`, `src="http://api.test/app/view/aaaaaaaaaaaa/_cover.svg"`)
	if strings.Contains(res.Body, "http://share.test/a/"+a.ID) || strings.Contains(res.Body, `action="`+detail+`/comments-enabled"`) {
		t.Fatal("private detail exposes public viewer link or offers unavailable viewer comments")
	}
	wantRedirect(t, e.post(detail+"/comments/th_private/reply", detail, url.Values{"body": {"Owner reply while private"}}), detail+"#th_private")
	want(t, e.get(detail), 200, "Owner reply while private")
	wantRedirect(t, e.post(detail+"/rollback/1", detail, nil), detail)
	got, _ = e.st.Artifact(ctx, a.ID)
	if got.Visibility != "private" || got.CurrentVersion != 3 {
		t.Fatalf("rollback changed visibility: %+v", got)
	}

	// The side peek remains open and receives the updated links and badge.
	res = e.post(action, detail, url.Values{"visibility": {"public"}, "panel": {"1"}})
	want(t, res, 200, `class="peek-inner"`, ">Public</span>", `src="http://share.test/a/aaaaaaaaaaaa/"`, "Owner reply while private")
	if strings.Contains(res.Body, "<html") || strings.Contains(res.Body, "Viewer comments are unavailable") {
		t.Fatal("visibility form lost panel mode or private-only copy remained")
	}
	want(t, e.post(action, detail, url.Values{"visibility": {"public"}, "panel": {"1"}}), 200)
}

func TestArtifactVisibilityGalleryAndOlderRows(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	e.daysAgo(10)
	old := e.publish("aaaaaaaaaaaa", "Older private draft", "")
	e.daysAgo(0)
	recent := e.publish("bbbbbbbbbbbb", "Recent private draft", "")
	public := e.publish("cccccccccccc", "Public page", "")
	for _, id := range []string{old.ID, recent.ID} {
		if err := e.st.SetVisibility(context.Background(), id, "private"); err != nil {
			t.Fatal(err)
		}
	}
	res := e.get("/app/artifacts")
	want(t, res, 200, "Older private draft", "Recent private draft", ">Private</span>", ">Public</span>")
	for _, id := range []string{old.ID, recent.ID} {
		want(t, res, 200, `href="http://api.test/app/view/`+id+`/"`, `src="http://api.test/app/view/`+id+`/_cover.svg"`)
		if strings.Contains(res.Body, "http://share.test/a/"+id) {
			t.Fatalf("gallery exposes public URL for private artifact %s", id)
		}
	}
	want(t, res, 200, `href="http://share.test/a/`+public.ID+`/"`)
	want(t, e.get("/app/artifacts?q=Recent"), 200, ">Private</span>", `src="http://api.test/app/view/`+recent.ID+`/_cover.svg"`)
}

func TestPortalHidesPrivateArtifacts(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	e.becomeIntegrator("Owner workspace")
	public := e.publish("aaaaaaaaaaaa", "Public report", "customer")
	private := e.publish("bbbbbbbbbbbb", "Confidential draft title", "customer")
	e.version(private.ID, 1, 100)
	e.thread(private.ID, "th_secret", "Confidential comment body")
	if err := e.st.SetVisibility(context.Background(), private.ID, "private"); err != nil {
		t.Fatal(err)
	}
	_, tenant := e.me()
	token := portal.Sign(e.h.Cfg.KeySecret, tenant.ID, *private.EndUserID, e.clock.t.Add(PortalLinkTTL))
	res := e.do(newClient(t), "GET", "/p/"+token, nil)
	want(t, res, 200, public.Title, "http://share.test/a/"+public.ID+"/")
	for _, secret := range []string{private.ID, private.Title, "Confidential comment body", "/app/view/"} {
		if strings.Contains(res.Body, secret) {
			t.Fatalf("portal leaked %q", secret)
		}
	}
	if err := e.st.SetVisibility(context.Background(), public.ID, "private"); err != nil {
		t.Fatal(err)
	}
	want(t, e.do(newClient(t), "GET", "/p/"+token, nil), 200, "No artifacts yet")
}
