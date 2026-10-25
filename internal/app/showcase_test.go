package app

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/store"
)

func TestShowcaseEmptyUntilConfigured(t *testing.T) {
	e := fixture(t)
	res := e.getHost("share.test", "/showcase")
	want(t, res, 200, "Pages built with byagent.", "The first pages are on their way", `aria-current="page">Showcase`, "Copy the prompt")
}

func TestShowcaseShowsCuratedPagesWithComments(t *testing.T) {
	e := courseFixture(t)
	path := collectionPath("learnpath: Async Rust")
	wantRedirect(t, e.post(path+"/share", path, url.Values{"on": {"1"}}), path)
	m := regexp.MustCompile(`http://share\.test/s/([A-Za-z0-9]+)/`).FindStringSubmatch(e.get(path).Body)
	if m == nil {
		t.Fatal("share link missing")
	}
	e.h.Cfg.ShowcaseSlug = m[1]
	if err := e.st.SetVisibility(context.Background(), "course000002", "private"); err != nil {
		t.Fatal(err)
	}
	e.thread("course000001", "th_one", "Can we <b>split</b> this?")
	if err := e.st.AddComment(context.Background(), store.Comment{ID: "cm_reply", ThreadID: "th_one", AuthorName: "Agent", AuthorKind: "owner", Body: "Split it in v2.", CreatedAt: e.clock.t}); err != nil {
		t.Fatal(err)
	}

	res := e.do(newClient(t), "GET", "/showcase", nil)
	want(t, res, 200,
		"Skill map", "Lesson two", // the public pages of the collection
		`href="http://share.test/a/course000001/"`,
		"hello world",                           // the quoted line
		"Can we &lt;b&gt;split&lt;/b&gt; this?", // the reader's comment, escaped
		"Split it in v2.",                       // and the answer under it
		"1 thread")
	for _, leak := range []string{"Lesson one", "Seat map", "Landing pitch"} {
		if strings.Contains(res.Body, leak) {
			t.Errorf("showcase shows %q, which is private or outside the collection", leak)
		}
	}
}

func TestShowcasePicksPagesByIDInOrder(t *testing.T) {
	e := courseFixture(t)
	if err := e.st.SetVisibility(context.Background(), "course000002", "private"); err != nil {
		t.Fatal(err)
	}
	e.h.Cfg.ShowcasePages = []string{"solo00000001", "course000002", "nope00000000", "travel000001"}
	e.thread("solo00000001", "th_noise", "tested")
	e.thread("travel000001", "th_real", "Can the seat legend sit under the map?")

	res := e.do(newClient(t), "GET", "/showcase", nil)
	want(t, res, 200, "Landing pitch", "Seat map", "Can the seat legend sit under the map?")
	if i, j := strings.Index(res.Body, "Landing pitch"), strings.Index(res.Body, "Seat map"); i > j {
		t.Error("pages are not in the configured order")
	}
	for _, leak := range []string{"Lesson one", ">tested<"} {
		if strings.Contains(res.Body, leak) {
			t.Errorf("showcase shows %q", leak)
		}
	}
}
