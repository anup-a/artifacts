package api

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

// publishInto publishes a one-file artifact labelled with project and returns
// its id. An empty project publishes it unlabelled.
func publishInto(t *testing.T, srvURL, tok, project, body string) string {
	t.Helper()
	extra := map[string]any{"title": body}
	if project != "" {
		extra["project"] = project
	}
	code, res := do(t, publishWith(t, "POST", srvURL+"/api/artifacts", tok, extra, []pubFile{{"index.html", "<p>" + body + "</p>"}}))
	if code != 201 {
		t.Fatalf("publish %s: %d %v", body, code, res)
	}
	// Publish times are the collection order; keep them strictly apart.
	time.Sleep(5 * time.Millisecond)
	return res["id"].(string)
}

func tenantOf(t *testing.T, d Deps, tok string) string {
	t.Helper()
	if tok == "admin-token" {
		k, err := d.Store.KeyByHash(context.Background(), ids.HashKey("admin:"+tok))
		if err != nil {
			t.Fatal(err)
		}
		return k.TenantID
	}
	k, err := d.Store.KeyByHash(context.Background(), ids.HashKey(tok))
	if err != nil {
		t.Fatal(err)
	}
	return k.TenantID
}

func idsOf(t *testing.T, list any) []string {
	t.Helper()
	out := []string{}
	for _, a := range list.([]any) {
		out = append(out, a.(map[string]any)["id"].(string))
	}
	return out
}

func TestListCollections(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	a1 := publishInto(t, srv.URL, tok, "Course", "one")
	a2 := publishInto(t, srv.URL, tok, "Course", "two")
	o1 := publishInto(t, srv.URL, tok, "Other", "other")
	publishInto(t, srv.URL, tok, "", "loose")
	a3 := publishInto(t, srv.URL, tok, "Course", "three")

	code, body := do(t, authReq(t, "GET", srv.URL+"/api/collections", tok))
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	list := body["collections"].([]any)
	if len(list) != 2 {
		t.Fatalf("an unlabelled artifact is not a collection: %v", list)
	}
	course, other := list[0].(map[string]any), list[1].(map[string]any)
	if course["name"] != "Course" || other["name"] != "Other" {
		t.Fatalf("most recently active first: %v", list)
	}
	if course["count"] != float64(3) || course["description"] != "" || course["share_url"] != nil {
		t.Fatalf("course: %v", course)
	}
	if got := idsOf(t, course["latest"]); !reflect.DeepEqual(got, []string{a3, a2, a1}) {
		t.Fatalf("latest: %v", got)
	}
	// Latest artifacts render as every other endpoint renders an artifact.
	_, a3JSON := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+a3, tok))
	first := course["latest"].([]any)[0].(map[string]any)
	if first["url"] != a3JSON["url"] || first["title"] != "three" || first["project"] != "Course" || first["cover_url"] != a3JSON["cover_url"] {
		t.Fatalf("latest artifact json: %v", first)
	}
	if course["updated_at"] != a3JSON["updated_at"] {
		t.Fatalf("updated_at is the newest artifact's: %v vs %v", course["updated_at"], a3JSON["updated_at"])
	}
	if _, err := time.Parse(time.RFC3339, course["updated_at"].(string)); err != nil {
		t.Fatalf("updated_at must be RFC3339: %v", err)
	}
	if got := idsOf(t, other["latest"]); !reflect.DeepEqual(got, []string{o1}) {
		t.Fatalf("other latest: %v", got)
	}

	// Saved metadata shows up, and a share slug becomes the public index URL.
	tenant := tenantOf(t, d, tok)
	if err := d.Store.SaveCollection(context.Background(), store.Collection{TenantID: tenant, Name: "Course", Description: "Async Rust", ShareSlug: "slug12345678"}, d.Now()); err != nil {
		t.Fatal(err)
	}
	_, body = do(t, authReq(t, "GET", srv.URL+"/api/collections", tok))
	course = body["collections"].([]any)[0].(map[string]any)
	if course["description"] != "Async Rust" || course["share_url"] != "http://share.test/s/slug12345678/" {
		t.Fatalf("saved metadata: %v", course)
	}
}

func TestListCollectionsIsTenantScoped(t *testing.T) {
	srv, d, _ := newTestServer(t, true)
	mine := mintKey(t, d, "personal")
	publishInto(t, srv.URL, mine, "Course", "one")

	code, body := do(t, authReq(t, "GET", srv.URL+"/api/collections", mintKey(t, d, "personal")))
	if code != 200 || !reflect.DeepEqual(body["collections"], []any{}) {
		t.Fatalf("another tenant sees nothing, as an empty list: %d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/collections", mintKeyIn(t, d, tenantOf(t, d, mine))))
	if code != 200 || len(body["collections"].([]any)) != 1 {
		t.Fatalf("a second key of the same tenant sees it: %d %v", code, body)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/collections", nil)
	if code, _ := do(t, req); code != 401 {
		t.Fatalf("no key: %d", code)
	}
}

func TestGetCollectionCourseOrder(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	name := "Hi Travel/Q3"
	a1 := publishInto(t, srv.URL, tok, name, "one")
	a2 := publishInto(t, srv.URL, tok, name, "two")
	a3 := publishInto(t, srv.URL, tok, name, "three")
	publishInto(t, srv.URL, tok, "Other", "other")
	// Republishing the first page moves its updated_at, not its place.
	if code, _ := do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+a1, tok, nil, []pubFile{{"index.html", "<p>one again</p>"}})); code != 200 {
		t.Fatalf("republish: %d", code)
	}
	path := srv.URL + "/api/collections/" + url.PathEscape(name)

	code, body := do(t, authReq(t, "GET", path, tok))
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if got := idsOf(t, body["artifacts"]); !reflect.DeepEqual(got, []string{a1, a2, a3}) {
		t.Fatalf("publish order: %v", got)
	}
	_, a1JSON := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+a1, tok))
	if body["name"] != name || body["count"] != float64(3) || body["description"] != "" || body["share_url"] != nil || body["updated_at"] != a1JSON["updated_at"] {
		t.Fatalf("metadata: %v", body)
	}
	first := body["artifacts"].([]any)[0].(map[string]any)
	if first["url"] != a1JSON["url"] || first["current_version"] != float64(2) {
		t.Fatalf("artifact json: %v", first)
	}

	// Saved positions lead, unknown ids are skipped, the rest follow in publish order.
	tenant := tenantOf(t, d, tok)
	c := store.Collection{TenantID: tenant, Name: name, Description: "trips", Positions: []string{a3, "gone00000001", a1, a3}, ShareSlug: "travelslug01"}
	if err := d.Store.SaveCollection(context.Background(), c, d.Now()); err != nil {
		t.Fatal(err)
	}
	_, body = do(t, authReq(t, "GET", path, tok))
	if got := idsOf(t, body["artifacts"]); !reflect.DeepEqual(got, []string{a3, a1, a2}) {
		t.Fatalf("course order: %v", got)
	}
	if body["description"] != "trips" || body["share_url"] != "http://share.test/s/travelslug01/" {
		t.Fatalf("saved metadata: %v", body)
	}
}

func TestGetCollectionNotFound(t *testing.T) {
	srv, d, _ := newTestServer(t, true)
	mine := mintKey(t, d, "personal")
	id := publishInto(t, srv.URL, mine, "Course", "one")

	for _, name := range []string{"Nope", "course"} {
		code, body := do(t, authReq(t, "GET", srv.URL+"/api/collections/"+url.PathEscape(name), mine))
		errBody, _ := body["error"].(map[string]any)
		if code != 404 || errBody["code"] != "NOT_FOUND" {
			t.Fatalf("%s: %d %v", name, code, body)
		}
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/collections/Course", mintKey(t, d, "personal"))); code != 404 {
		t.Fatalf("another tenant's collection: %d", code)
	}

	// Once its last artifact is deleted, a label without a saved row is gone...
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, mine)); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/collections/Course", mine)); code != 404 {
		t.Fatalf("empty label: %d", code)
	}
	// ...but a saved row keeps it, empty.
	if err := d.Store.SaveCollection(context.Background(), store.Collection{TenantID: tenantOf(t, d, mine), Name: "Course", Description: "kept"}, d.Now()); err != nil {
		t.Fatal(err)
	}
	code, body := do(t, authReq(t, "GET", srv.URL+"/api/collections/Course", mine))
	if code != 200 || body["count"] != float64(0) || !reflect.DeepEqual(body["artifacts"], []any{}) || body["description"] != "kept" || body["updated_at"] == nil {
		t.Fatalf("saved empty collection: %d %v", code, body)
	}
}

func TestCourseOrder(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	art := func(id string, minute int) store.Artifact {
		return store.Artifact{ID: id, CreatedAt: base.Add(time.Duration(minute) * time.Minute)}
	}
	// ListArtifacts hands them over newest-updated first.
	in := []store.Artifact{art("c", 3), art("a", 1), art("b", 2), art("d", 1)}
	got := func(positions []string) []string {
		out := []string{}
		for _, a := range courseOrder(in, positions) {
			out = append(out, a.ID)
		}
		return out
	}
	if g := got(nil); !reflect.DeepEqual(g, []string{"a", "d", "b", "c"}) {
		t.Fatalf("publish order, ties by id: %v", g)
	}
	if g := got([]string{"c", "x", "b", "c"}); !reflect.DeepEqual(g, []string{"c", "b", "a", "d"}) {
		t.Fatalf("positions first: %v", g)
	}
	if in[0].ID != "c" {
		t.Fatalf("input must not be reordered: %v", in)
	}
}
