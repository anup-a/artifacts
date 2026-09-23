package app

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// courseFixture publishes a small workspace: a three-page course, a
// two-page project with a one-page near-duplicate label, and a loner.
func courseFixture(t *testing.T) *env {
	t.Helper()
	e := fixture(t)
	e.signIn("a@x.io")
	e.daysAgo(0)
	for _, p := range []struct{ id, title, project string }{
		{"course000001", "Skill map", "learnpath: Async Rust"},
		{"course000002", "Lesson one", "learnpath: Async Rust"},
		{"course000003", "Lesson two", "learnpath: Async Rust"},
		{"travel000001", "Seat map", "Hi Travel"},
		{"travel000002", "Hotel card", "Hi Travel"},
		{"travel000003", "Old hotel note", "Hi-Travel"},
		{"solo00000001", "Landing pitch", "Creatica"},
	} {
		e.publish(p.id, p.title, "")
		e.label(p.id, p.project)
	}
	return e
}

func collectionPath(name string) string { return "/app/collections/" + url.PathEscape(name) }

func TestArtifactsShowsCollectionShelf(t *testing.T) {
	e := courseFixture(t)
	res := e.get("/app/artifacts")
	want(t, res, 200,
		`class="shelf"`,
		`class="deck" href="/app/collections/learnpath:%20Async%20Rust"`,
		`<span class="deck-count">3</span>`,
		`class="deck" href="/app/collections/Hi%20Travel"`,
		`src="http://share.test/a/course000003/_cover.svg"`,
		// Creatica has one artifact: a chip, never a one-card deck.
		`class="pchip" href="/app/collections/Creatica"`,
		// Hi-Travel and Hi Travel normalise to the same name: offer the merge.
		`action="/app/collections/Hi-Travel/rename"`, `name="to" value="Hi Travel"`,
	)
	if strings.Contains(res.Body, `class="deck" href="/app/collections/Creatica"`) {
		t.Fatal("a single artifact must not render as a deck")
	}
	if n := count(res.Body, `class="deck-card`); n != 5 {
		t.Fatalf("a deck of three and a deck of two, got %d cards", n)
	}
	filtered := e.get("/app/artifacts?project=Hi%20Travel")
	if strings.Contains(filtered.Body, `class="shelf"`) {
		t.Fatal("a filtered list is already one collection: no shelf")
	}
	want(t, e.get("/app/artifacts"), 200, `href="/app/collections" `)
}

func TestCollectionsIndexListsEveryCollection(t *testing.T) {
	e := courseFixture(t)
	res := e.get("/app/collections")
	want(t, res, 200, `<h1 class="page-title">Collections</h1>`,
		`href="/app/collections/learnpath:%20Async%20Rust"`, `href="/app/collections/Creatica"`, `href="/app/collections/Hi-Travel"`,
		`aria-current="page"`)
}

var rowOrder = regexp.MustCompile(`data-artifact="([a-z0-9]+)"`)

func order(body string) []string {
	out := []string{}
	for _, m := range rowOrder.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestCollectionPageOrderDescriptionAndMove(t *testing.T) {
	e := courseFixture(t)
	path := collectionPath("learnpath: Async Rust")
	res := e.get(path)
	want(t, res, 200, `learnpath: Async Rust`, `3 artifacts`, `Course order`, `Skill map`)
	if got := strings.Join(order(res.Body), ","); got != "course000001,course000002,course000003" {
		t.Fatalf("course order is publish order: %s", got)
	}
	if got := strings.Join(order(e.get(path+"?sort=newest").Body), ","); got != "course000003,course000002,course000001" {
		t.Fatalf("newest first: %s", got)
	}

	wantRedirect(t, e.post(path+"/meta", path, url.Values{"description": {"  Async Rust for a TypeScript developer  "}}), path)
	want(t, e.get(path), 200, `Async Rust for a TypeScript developer</p>`)

	wantRedirect(t, e.post(path+"/move", path, url.Values{"id": {"course000001"}, "dir": {"down"}}), path)
	if got := strings.Join(order(e.get(path).Body), ","); got != "course000002,course000001,course000003" {
		t.Fatalf("moved down one place: %s", got)
	}
	// A newly published page joins at the end of a custom order.
	e.publish("course000004", "Lesson three", "")
	e.label("course000004", "learnpath: Async Rust")
	if got := strings.Join(order(e.get(path).Body), ","); got != "course000002,course000001,course000003,course000004" {
		t.Fatalf("new pages follow the saved order: %s", got)
	}

	long := strings.Repeat("x", 501)
	want(t, e.post(path+"/meta", path, url.Values{"description": {long}}), 400)
	want(t, e.get("/app/collections/Nope"), 404)
}

func TestCollectionShareAndPublicIndex(t *testing.T) {
	e := courseFixture(t)
	path := collectionPath("learnpath: Async Rust")
	private := "course000002"
	if err := e.st.SetVisibility(context.Background(), private, "private"); err != nil {
		t.Fatal(err)
	}
	wantRedirect(t, e.post(path+"/share", path, url.Values{"on": {"1"}}), path)
	page := e.get(path)
	m := regexp.MustCompile(`http://share\.test/s/([A-Za-z0-9]+)/`).FindStringSubmatch(page.Body)
	if m == nil {
		t.Fatalf("share link missing:\n%s", page.Body)
	}
	public := "/s/" + m[1] + "/"

	anon := e.do(newClient(t), "GET", public, nil)
	want(t, anon, 200, `learnpath: Async Rust`, `href="http://share.test/a/course000001/"`, `href="http://share.test/a/course000003/"`)
	if strings.Contains(anon.Body, private) {
		t.Fatal("the public index must never list a private artifact")
	}
	if strings.Contains(anon.Body, `name="csrf"`) || strings.Contains(anon.Body, "sidebar") {
		t.Fatal("the public index carries no session chrome")
	}
	if got := anon.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("public index cache: %q", got)
	}

	wantRedirect(t, e.post(path+"/share", path, url.Values{}), path)
	want(t, e.do(newClient(t), "GET", public, nil), 404)
	want(t, e.do(newClient(t), "GET", "/s/doesnotexist/", nil), 404)
}

func TestCollectionRenameAndMerge(t *testing.T) {
	e := courseFixture(t)
	wantRedirect(t, e.post("/app/collections/Hi-Travel/rename", "/app/artifacts", url.Values{"to": {"Hi Travel"}}), collectionPath("Hi Travel"))
	res := e.get(collectionPath("Hi Travel"))
	want(t, res, 200, `3 artifacts`, `Old hotel note`)
	want(t, e.get("/app/collections/Hi-Travel"), 404)

	wantRedirect(t, e.post(collectionPath("Hi Travel")+"/rename", collectionPath("Hi Travel"), url.Values{"to": {"Travel"}}), "/app/collections/Travel")
	want(t, e.post("/app/collections/Travel/rename", "/app/collections/Travel", url.Values{"to": {"   "}}), 400)
}

func TestCollectionsAreTenantScoped(t *testing.T) {
	e := courseFixture(t)
	e.becomeIntegrator("Other workspace")
	res := e.get("/app/collections")
	if strings.Contains(res.Body, "Async Rust") {
		t.Fatal("another workspace must not see these collections")
	}
	want(t, e.get(collectionPath("learnpath: Async Rust")), 404)
}
