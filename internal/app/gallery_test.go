package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// label sets project and tags on an artifact straight in the store.
func (e *env) label(id, project string, tags ...string) {
	e.t.Helper()
	var p *string
	if project != "" {
		p = &project
	}
	if err := e.st.SetMetadata(context.Background(), id, p, tags, nil, ""); err != nil {
		e.t.Fatal(err)
	}
}

// versionWith stores version n of an artifact with the given file paths.
func (e *env) versionWith(id string, n int, paths ...string) {
	e.t.Helper()
	ctx := context.Background()
	files := make([]store.File, 0, len(paths))
	for i, p := range paths {
		sha := strings.Repeat("b", 62) + string(rune('0'+n)) + string(rune('0'+i))
		if err := e.st.UpsertBlob(ctx, sha, 10); err != nil {
			e.t.Fatal(err)
		}
		files = append(files, store.File{Path: p, SHA256: sha, Size: 10, ContentType: "application/octet-stream"})
	}
	v := store.Version{ArtifactID: id, Version: n, CreatedAt: e.clock.t, Bytes: int64(10 * len(paths)), FileCount: len(paths)}
	if err := e.st.AddVersion(ctx, v, files); err != nil {
		e.t.Fatal(err)
	}
}

func count(s, sub string) int { return strings.Count(s, sub) }

func TestArtifactsSectionsAndHero(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	e.daysAgo(10)
	e.publish("old000000001", "Old one", "")
	e.daysAgo(1)
	e.publish("yest00000001", "Yesterday one", "")
	e.publish("yest00000002", "Yesterday two", "")
	e.daysAgo(0)
	for i, id := range []string{"today0000001", "today0000002", "today0000003", "today0000004", "today0000005"} {
		e.publish(id, "Today "+string(rune('a'+i)), "")
	}
	e.label("today0000001", "Hi Travel", "report", "quarterly", "third")
	e.label("old000000001", "Distill")
	e.daysAgo(1)
	e.publish("yest00000003", "", "") // untitled: the tile and the cover fall back to the id
	e.daysAgo(0)

	res := e.get("/app/artifacts")
	want(t, res, 200, `<h2 class="day-label">Today</h2>`, `<h2 class="day-label">Yesterday</h2>`, `<h2 class="day-label">Older</h2>`,
		`data-day="2026-09-16"`, `data-at="2026-09-16T12:00:04Z"`, `data-day="2026-09-15"`,
		`class="tile is-hero"`, `class="tile-cover" href="/app/artifacts/today0000005" data-peek aria-label="Today e" title="Today e"`,
		`class="tile-cover" href="/app/artifacts/yest00000003" data-peek aria-label="yest00000003" title="Untitled: yest00000003"`,
		`<img src="http://share.test/a/today0000005/_cover.svg" alt="" loading="lazy"`,
		`<span class="dot" data-color="#`, `>Hi Travel</span></a>`, `<span class="tag">report</span><span class="tag">quarterly</span>`,
		`class="icon-btn tile-open" href="http://share.test/a/today0000001/"`,
		`<th class="c-project">Project</th>`, `class="thumb" src="http://share.test/a/old000000001/_cover.svg"`, ">Distill</a>", "Old one", "9 artifacts")
	if n := count(res.Body, `class="tile is-hero"`); n != 1 {
		t.Fatalf("%d heroes, want 1", n)
	}
	if strings.Contains(res.Body, `is-wide`) || strings.Contains(res.Body, `tile-title`) {
		t.Fatal("tiles must be a single anatomy: cover plus meta, no wide span, no duplicate title")
	}
	if strings.Contains(res.Body, `<span class="tag">third</span>`) {
		t.Fatal("card shows more than two tags")
	}
	// The newest of today is the hero, and it is the first tile on the page.
	hero := regexp.MustCompile(`class="tile is-hero"[\s\S]*?href="/app/artifacts/([a-z0-9]+)"`).FindStringSubmatch(res.Body)
	if hero == nil || hero[1] != "today0000005" {
		t.Fatalf("hero %v", hero)
	}
	if strings.Index(res.Body, "Yesterday one") < strings.Index(res.Body, "Today a") {
		t.Fatal("yesterday rendered before today")
	}
	if strings.Contains(res.Body, `href="/app/artifacts/old000000001" data-peek aria-label`) {
		t.Fatal("older artifact rendered as a tile")
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "img-src 'self' data: http://share.test") {
		t.Fatalf("csp lacks the share origin for covers: %s", res.Header.Get("Content-Security-Policy"))
	}
}

func TestArtifactsProjectChips(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	e.publish("aaaaaaaaaaaa", "Travel one", "")
	e.publish("bbbbbbbbbbbb", "Travel two", "")
	e.publish("cccccccccccc", "Distill one", "")
	e.publish("dddddddddddd", "Loose", "")
	e.label("aaaaaaaaaaaa", "Hi Travel")
	e.label("bbbbbbbbbbbb", "Hi Travel")
	e.label("cccccccccccc", "Distill")

	res := e.get("/app/artifacts")
	want(t, res, 200, `class="pchip is-on" href="/app/artifacts" aria-current="true">All</a>`,
		`class="pchip" href="/app/artifacts?project=Hi%20Travel"`, `Hi Travel<span class="pchip-n">2</span>`, `Distill<span class="pchip-n">1</span>`)
	if strings.Contains(res.Body, "This week") {
		t.Fatal("unfiltered page uses the flat grid")
	}

	res = e.get("/app/artifacts?project=Hi%20Travel")
	want(t, res, 200, `class="pchip is-on" href="/app/artifacts?project=Hi%20Travel" aria-current="true"`, "Travel one", "Travel two", `<h2 class="day-label">This week</h2>`, "2 artifacts")
	for _, s := range []string{"Distill one", "Loose", `class="tile is-hero"`, `<h2 class="day-label">Today</h2>`} {
		if strings.Contains(res.Body, s) {
			t.Fatalf("filtered page contains %q", s)
		}
	}
	// Search keeps the project filter and vice versa.
	res = e.get("/app/artifacts?project=Hi%20Travel&q=two")
	want(t, res, 200, "Travel two", `name="project" value="Hi Travel"`, `href="/app/artifacts?project=Hi%20Travel&amp;q=two" aria-current="true"`)
	if strings.Contains(res.Body, "Travel one") {
		t.Fatal("search ignored with a project filter")
	}
	want(t, e.get("/app/artifacts?project=Nope"), 200, "No artifacts match", "different search or project")

	// A workspace without labels shows no chip bar at all.
	other := fixture(t)
	other.signIn("b@x.io")
	other.publish("eeeeeeeeeeee", "Plain", "")
	if strings.Contains(other.get("/app/artifacts").Body, `class="chips"`) {
		t.Fatal("chip bar shown without projects")
	}
}

func TestArtifactsPaginationKeepsFilters(t *testing.T) {
	d := artifactsData{EndUser: "c/1", Query: "q x", Project: "Hi Travel"}
	if got := d.PageURL("abc"); got != "/app/artifacts?cursor=abc&end_user=c%2F1&project=Hi%20Travel&q=q%20x" {
		t.Fatalf("PageURL %q", got)
	}
	if got := d.ChipURL(""); got != "/app/artifacts?end_user=c%2F1&q=q%20x" {
		t.Fatalf("ChipURL %q", got)
	}
	if got := (artifactsData{}).PageURL(""); got != "/app/artifacts" {
		t.Fatalf("bare %q", got)
	}
	// Recent items past the first page render as rows, never as tiles.
	e := fixture(t)
	e.signIn("a@x.io")
	for i := 0; i < pageSize+1; i++ {
		e.publish(fmt.Sprintf("art%09d", i), "Item", "")
	}
	res := e.get("/app/artifacts")
	next := regexp.MustCompile(`href="/app/artifacts\?cursor=([^"&]+)"`).FindStringSubmatch(res.Body)
	if next == nil {
		t.Fatal("no cursor link")
	}
	res = e.get("/app/artifacts?cursor=" + next[1])
	want(t, res, 200, `class="row-title"`)
	if strings.Contains(res.Body, `class="tile`) {
		t.Fatal("second page rendered tiles")
	}
}

func TestDayLabel(t *testing.T) {
	now := time.Date(2026, 9, 16, 23, 30, 0, 0, time.UTC)
	cases := map[time.Time]string{
		time.Date(2026, 9, 16, 0, 1, 0, 0, time.UTC):   "Today",
		time.Date(2026, 9, 15, 23, 59, 0, 0, time.UTC): "Yesterday",
		time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC):  "Mon 14 Sep",
		time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC):  "Thu 10 Sep",
	}
	for day, s := range cases {
		if got := dayLabel("en", day, now); got != s {
			t.Errorf("dayLabel(%v) = %q, want %q", day, got, s)
		}
	}
	// Rows are four cells; a hero is four cells; trailing cards widen so the
	// last row ends flush.
	shapes := func(n int, hero bool) string {
		var got []string
		for _, c := range spans(make([]cardView, n), hero) {
			got = append(got, c.Span)
		}
		return strings.Join(got, ",")
	}
	shapeCases := map[string]string{
		"8,hero":  "hero,,,,,,,",
		"1,hero":  "hero",
		"3,plain": ",,",
		"0,hero":  "",
	}
	for k, want := range shapeCases {
		var n int
		var mode string
		if _, err := fmt.Sscanf(k, "%d,%s", &n, &mode); err != nil {
			t.Fatal(err)
		}
		if got := shapes(n, mode == "hero"); got != want {
			t.Errorf("spans(%s) = %q, want %q", k, got, want)
		}
	}
	if s := spans([]cardView{{}, {}, {}, {}}, false); s[0].Span != "" {
		t.Fatalf("hero without hero flag: %q", s[0].Span)
	}
}

func TestArtifactMetaForm(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	a := e.publish("aaaaaaaaaaaa", "Quarterly report", "")
	e.versionWith(a.ID, 1, "index.html", "cover.png", "notes.txt")
	e.publish("bbbbbbbbbbbb", "Other", "")
	e.label("bbbbbbbbbbbb", "Distill")
	detail := "/app/artifacts/aaaaaaaaaaaa"
	action := detail + "/meta"

	// Fresh artifact: generated cover with the id-derived style, empty labels,
	// the workspace's projects in the datalist and the image file offered.
	res := e.get(detail)
	want(t, res, 200, `id="about"`, `action="`+action+`"`, `name="project" value=""`, `name="tags" value=""`,
		`value="generated" checked`, `<option value="Distill"></option>`, `<option value="cover.png"`, `<option value="dots"`, `<option value="plain"`,
		`class="detail-cover" href="http://share.test/a/aaaaaaaaaaaa/_cover.svg"`)
	if strings.Contains(res.Body, `<option value="index.html"`) || strings.Contains(res.Body, `<option value="notes.txt"`) {
		t.Fatal("non-image files offered as covers")
	}

	// Save: labels normalised the way the API does, redirect to the card.
	before, _ := e.st.Artifact(context.Background(), a.ID)
	form := url.Values{"project": {"  Hi Travel "}, "tags": {"Report, quarterly,, REPORT ,"}, "cover_kind": {"generated"}, "cover_style": {"rings"}}
	wantRedirect(t, e.post(action, detail, form), detail+"#about")
	got, err := e.st.Artifact(context.Background(), a.ID)
	if err != nil || got.Project == nil || *got.Project != "Hi Travel" || strings.Join(got.Tags, ",") != "report,quarterly" || got.CoverPath != nil || got.CoverStyle != "rings" {
		t.Fatalf("after save: %+v %v", got, err)
	}
	if !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("relabelling touched updated_at")
	}
	res = e.get(detail)
	want(t, res, 200, `name="project" value="Hi Travel"`, `name="tags" value="report, quarterly"`, `value="rings" selected`, `>Hi Travel</a>`, `<span class="tag">report</span>`)
	want(t, e.get("/app/artifacts"), 200, `Hi Travel<span class="pchip-n">1</span>`)

	// A file cover from the current version.
	form = url.Values{"project": {"Hi Travel"}, "tags": {""}, "cover_kind": {"file"}, "cover_style": {"rings"}, "cover_file": {"cover.png"}}
	wantRedirect(t, e.post(action, detail, form), detail+"#about")
	got, _ = e.st.Artifact(context.Background(), a.ID)
	if got.CoverPath == nil || *got.CoverPath != "cover.png" || len(got.Tags) != 0 {
		t.Fatalf("file cover: %+v", got)
	}
	want(t, e.get(detail), 200, `value="file" checked`, `<option value="cover.png" selected`)

	// Validation errors re-render with the API's messages and the typed values.
	bad := []struct {
		form url.Values
		msg  string
	}{
		{url.Values{"project": {strings.Repeat("x", 65)}}, "project must be 1 to 64 characters"},
		{url.Values{"project": {"bad\x01name"}}, "project must not contain control characters"},
		{url.Values{"project": {"ok"}, "tags": {strings.Repeat("t", 33)}}, "tags[0] must be 1 to 32 characters"},
		{url.Values{"project": {"ok"}, "tags": {"a,b,c,d,e,f,g,h,i,j,k"}}, "tags: at most 10 allowed"},
		{url.Values{"project": {"ok"}, "cover_kind": {"generated"}, "cover_style": {"neon"}}, "cover_style must be one of dots, grid, lines, wash, rings, plain"},
		{url.Values{"project": {"ok"}, "cover_kind": {"file"}, "cover_file": {"notes.txt"}}, "cover must be an svg, png, jpg or webp file"},
		{url.Values{"project": {"ok"}, "cover_kind": {"file"}, "cover_file": {"missing.png"}}, "cover must name a file in the current version: missing.png"},
		{url.Values{"project": {"ok"}, "cover_kind": {"upload"}}, "The form could not be read."},
	}
	for _, c := range bad {
		res := e.post(action, detail, c.form)
		want(t, res, 400, `id="about"`, c.msg, `name="project" value="`+strings.ReplaceAll(c.form.Get("project"), "\x01", "\x01")[:2])
		if strings.Contains(res.Body, `<aside class="peek`) {
			t.Fatal("full-page post rendered a fragment")
		}
	}
	if got, _ = e.st.Artifact(context.Background(), a.ID); got.Project == nil || *got.Project != "Hi Travel" {
		t.Fatalf("invalid post changed the artifact: %+v", got)
	}

	// Panel mode: the fragment comes back in place of a redirect, for both
	// a successful save and a validation error.
	form = url.Values{"project": {"Distill"}, "tags": {"video"}, "cover_kind": {"generated"}, "cover_style": {"wash"}, "panel": {"1"}}
	res = e.post(action, detail, form)
	want(t, res, 200, `class="peek-inner"`, `name="project" value="Distill"`, `value="wash" selected`, `<span class="tag">video</span>`)
	if strings.Contains(res.Body, `<aside class="sidebar">`) {
		t.Fatal("panel post rendered the shell")
	}
	res = e.post(action, detail, url.Values{"project": {strings.Repeat("y", 70)}, "panel": {"1"}})
	want(t, res, 400, `class="peek-inner"`, "project must be 1 to 64 characters", `value="`+strings.Repeat("y", 70)+`"`)

	// Clearing: an empty project means none; other tenants' ids are invisible.
	wantRedirect(t, e.post(action, detail, url.Values{"project": {""}, "tags": {""}, "cover_kind": {"generated"}, "cover_style": {"wash"}}), detail+"#about")
	if got, _ = e.st.Artifact(context.Background(), a.ID); got.Project != nil {
		t.Fatalf("project not cleared: %+v", got)
	}
	want(t, e.post("/app/artifacts/nope/meta", detail, url.Values{"project": {"x"}}), 404, "does not exist")
}
