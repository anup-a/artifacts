package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	// "Workspace link", not "Private link": with no share code this address
	// only opens for signed-in members, and the label has to say so.
	want(t, res, 200, ">Private</span>", "Workspace link", "signed-in members of the owning workspace", "Viewer comments are unavailable while this artifact is private", "Keep this discussion", `src="http://api.test/app/view/aaaaaaaaaaaa/"`, `src="http://api.test/app/view/aaaaaaaaaaaa/_cover.svg"`)
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
	want(t, res, 200, "Older private draft", "Recent private draft", ">Private</span>")
	// Public is the default: the gallery flags only the exception, so exactly
	// the two private tiles carry a badge and the public one carries none.
	if n := strings.Count(res.Body, ">Private</span>"); n != 2 {
		t.Fatalf("want a badge on each private tile, got %d", n)
	}
	if strings.Contains(res.Body, ">Public</span>") {
		t.Fatal("gallery labels the default state")
	}
	for _, id := range []string{old.ID, recent.ID} {
		want(t, res, 200, `href="http://api.test/app/view/`+id+`/"`, `src="http://api.test/app/view/`+id+`/_cover.svg"`)
		if strings.Contains(res.Body, "http://share.test/a/"+id) {
			t.Fatalf("gallery exposes public URL for private artifact %s", id)
		}
	}
	want(t, res, 200, `href="http://share.test/a/`+public.ID+`/"`)
	want(t, e.get("/app/artifacts?q=Recent"), 200, ">Private</span>", `src="http://api.test/app/view/`+recent.ID+`/_cover.svg"`)
}

// The page a refused private artifact address renders: it says what happened
// and offers a way in, rather than navigating the visitor to sign-in.
func TestDenyArtifactAccessPage(t *testing.T) {
	e := fixture(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app/view/{id}/", e.h.DenyArtifactAccess)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	res, err := newClient(t).Get(srv.URL + "/app/view/aaaaaaaaaaaa/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	got := string(body)
	if res.StatusCode != 404 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get("Location") != "" {
		t.Fatal("refusal redirected instead of explaining")
	}
	for _, s := range []string{"You do not have access to this artifact", `href="/app/login"`, `href="/app/"`} {
		if !strings.Contains(got, s) {
			t.Fatalf("page missing %q: %s", s, got)
		}
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache %v", res.Header)
	}
}

// The link the dashboard offers to copy is the one you would send somebody,
// which is not always the one the owner opens. Copying an /app/view address to
// a friend hands them a page only your workspace can load.
func TestDetailOffersTheSendableLink(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@x.io")
	a := e.publish("aaaaaaaaaaaa", "Shared draft", "")
	detail := "/app/artifacts/" + a.ID
	ctx := context.Background()

	// Public: the share origin, as before.
	want(t, e.get(detail), 200, `value="http://share.test/a/`+a.ID+`/"`, ">Share link<")

	// Private with no code: honestly labelled, because only the workspace can
	// open it. The owner's own Open link stays on the account origin.
	if err := e.st.SetVisibility(ctx, a.ID, "private"); err != nil {
		t.Fatal(err)
	}
	res := e.get(detail)
	want(t, res, 200, `value="http://api.test/app/view/`+a.ID+`/"`, ">Workspace link<")

	// With a code, the link worth copying is the public one carrying it.
	if err := e.st.SetShareCode(ctx, a.ID, "481523"); err != nil {
		t.Fatal(err)
	}
	res = e.get(detail)
	share := "http://share.test/a/" + a.ID + "/?k=481523"
	want(t, res, 200, `value="`+share+`"`, "Share link, code included",
		`data-copy-text="`+share+`"`, ">481523<")
	if strings.Contains(res.Body, `data-copy-text="http://api.test/app/view/`) {
		t.Fatal("a copy button still hands over the workspace-only link")
	}
	// The owner still opens and previews it on the account origin, which is the
	// only address their session works on.
	if !strings.Contains(res.Body, `href="http://api.test/app/view/`+a.ID+`/"`) ||
		!strings.Contains(res.Body, `src="http://api.test/app/view/`+a.ID+`/"`) {
		t.Fatal("owner lost their own view or preview link")
	}
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
