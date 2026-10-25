package api

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/config"
)

// publishWith (from comments_toggle_test.go) builds a publish request whose
// manifest carries extra keys such as project, tags, cover and cover_style.

func TestManifestGalleryValidation(t *testing.T) {
	base := `"files":[{"path":"index.html","sha256":"` + sha64("a") + `","size":1},{"path":"art/c.png","sha256":"` + sha64("b") + `","size":1}]`
	ok := map[string]string{
		"project":      `{"project":"  Demo  ",` + base + `}`,
		"null project": `{"project":null,` + base + `}`,
		"tags":         `{"tags":[" A ","b","a"],` + base + `}`,
		"cover":        `{"cover":"art/c.png",` + base + `}`,
		"style":        `{"cover_style":"rings",` + base + `}`,
	}
	for name, raw := range ok {
		m, e := parseManifest([]byte(raw), config64())
		if e != nil {
			t.Errorf("%s: %v", name, e)
			continue
		}
		switch name {
		case "project":
			if m.project == nil || *m.project != "Demo" {
				t.Errorf("project not trimmed: %v", m.project)
			}
		case "tags":
			if !reflect.DeepEqual(m.tags, []string{"a", "b"}) {
				t.Errorf("tags not normalised: %v", m.tags)
			}
		}
	}
	// The JSON carries an escaped BEL (backslash-u0007) so the decoded label
	// contains a control character.
	bel := "\\" + "u0007"
	ctrl := `{"project":"a` + bel + `b",` + base + `}`
	bad := map[string][2]string{
		"long project":  {`{"project":"` + strings.Repeat("p", 65) + `",` + base + `}`, "project"},
		"ctrl project":  {ctrl, "project"},
		"empty tag":     {`{"tags":["ok",""],` + base + `}`, "tags[1]"},
		"long tag":      {`{"tags":["` + strings.Repeat("t", 33) + `"],` + base + `}`, "tags[0]"},
		"too many tags": {`{"tags":["1","2","3","4","5","6","7","8","9","10","11"],` + base + `}`, "tags"},
		"missing cover": {`{"cover":"nope.png",` + base + `}`, "cover"},
		"html cover":    {`{"cover":"index.html",` + base + `}`, "cover"},
		"bad style":     {`{"cover_style":"plasma",` + base + `}`, "cover_style"},
	}
	for name, c := range bad {
		_, e := parseManifest([]byte(c[0]), config64())
		if e == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if e.Status != 400 || e.Code != "INVALID_ARGUMENT" || !strings.Contains(e.Message, c[1]) {
			t.Errorf("%s: %d %s %q (want field %q)", name, e.Status, e.Code, e.Message, c[1])
		}
	}
}

func TestPublishStoresLabelsAndListsProjects(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<html><body>g</body></html>"}, {"cover.svg", "<svg/>"}}
	code, body := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok,
		map[string]any{"title": "Gallery", "project": "Demo", "tags": []string{"B", "a", "b"}, "cover": "cover.svg", "cover_style": "dots"}, files))
	if code != 201 {
		t.Fatalf("%d %v", code, body)
	}
	id := body["id"].(string)
	if body["project"] != "Demo" || !reflect.DeepEqual(body["tags"], []any{"b", "a"}) || body["cover"] != "cover.svg" || body["cover_style"] != "dots" {
		t.Fatalf("publish response: %v", body)
	}
	if body["cover_url"] != "http://share.test/a/"+id+"/_cover.svg" {
		t.Fatalf("cover_url: %v", body["cover_url"])
	}

	code, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok))
	if code != 200 || got["project"] != "Demo" || !reflect.DeepEqual(got["tags"], []any{"b", "a"}) || got["cover_style"] != "dots" || got["cover"] != "cover.svg" {
		t.Fatalf("get: %d %v", code, got)
	}
	if got["published_at"] != got["created_at"] || got["published_at"] == nil {
		t.Fatalf("published_at must alias created_at: %v", got)
	}

	// The cover endpoint streams the publisher file.
	res, err := http.Get(srv.URL + "/a/" + id + "/_cover.svg")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(raw) != "<svg/>" || res.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("cover: %d %q %s", res.StatusCode, raw, res.Header.Get("Content-Type"))
	}

	// A second artifact without labels reads back with defaults.
	_, plain := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "p"}}, nil))
	if plain["project"] != nil || !reflect.DeepEqual(plain["tags"], []any{}) || plain["cover"] != nil || plain["cover_style"] == "" {
		t.Fatalf("defaults: %v", plain)
	}
	_, third := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"project": "Demo"}, []pubFile{{"index.html", "q"}}))

	code, projects := do(t, authReq(t, "GET", srv.URL+"/api/projects", tok))
	if code != 200 || !reflect.DeepEqual(projects["projects"], []any{map[string]any{"name": "Demo", "count": float64(2)}}) {
		t.Fatalf("projects: %d %v", code, projects)
	}
	code, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts?project=Demo", tok))
	ids := []string{}
	for _, a := range list["artifacts"].([]any) {
		ids = append(ids, a.(map[string]any)["id"].(string))
	}
	if code != 200 || !reflect.DeepEqual(ids, []string{third["id"].(string), id}) {
		t.Fatalf("filter: %d %v", code, ids)
	}
	if _, none := do(t, authReq(t, "GET", srv.URL+"/api/artifacts?project=Other", tok)); len(none["artifacts"].([]any)) != 0 {
		t.Fatalf("unknown project: %v", none)
	}
}

func TestPutKeepsClearsAndReplacesLabels(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<html><body>u</body></html>"}, {"c.png", "png"}}
	_, body := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok,
		map[string]any{"project": "P", "tags": []string{"x"}, "cover": "c.png", "cover_style": "wash"}, files))
	id := body["id"].(string)
	url := srv.URL + "/api/artifacts/" + id

	// Omitted keys keep everything.
	code, kept := do(t, publishWith(t, "PUT", url, tok, nil, files))
	if code != 200 || kept["project"] != "P" || !reflect.DeepEqual(kept["tags"], []any{"x"}) || kept["cover"] != "c.png" || kept["cover_style"] != "wash" {
		t.Fatalf("kept: %d %v", code, kept)
	}
	// null clears project and cover; [] clears tags.
	_, cleared := do(t, publishWith(t, "PUT", url, tok, map[string]any{"project": nil, "cover": nil, "tags": []string{}}, files))
	if cleared["project"] != nil || cleared["cover"] != nil || !reflect.DeepEqual(cleared["tags"], []any{}) || cleared["cover_style"] != "wash" {
		t.Fatalf("cleared: %v", cleared)
	}
	// Replace, and a cover missing from the new file set is a 400.
	_, replaced := do(t, publishWith(t, "PUT", url, tok, map[string]any{"project": "Q", "tags": []string{"y", "z"}}, files))
	if replaced["project"] != "Q" || !reflect.DeepEqual(replaced["tags"], []any{"y", "z"}) {
		t.Fatalf("replaced: %v", replaced)
	}
	code, bad := do(t, publishWith(t, "PUT", url, tok, map[string]any{"cover": "c.png"}, files[:1]))
	if code != 400 || bad["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("cover outside manifest: %d %v", code, bad)
	}
	// Rollback keeps labels (it rebuilds the manifest without them). Version 1
	// is already pruned (VersionsKept is 3), so roll back to 2.
	code, rb := do(t, authReq(t, "POST", url+"/rollback/2", tok))
	if code != 200 {
		t.Fatalf("rollback %d %v", code, rb)
	}
	_, after := do(t, authReq(t, "GET", url, tok))
	if after["project"] != "Q" || !reflect.DeepEqual(after["tags"], []any{"y", "z"}) {
		t.Fatalf("labels lost on rollback: %v", after)
	}
}

func TestServedPageHasCoverMeta(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, body := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "OG title", []pubFile{{"index.html", "<html><head></head><body>x</body></html>"}}, nil))
	id := body["id"].(string)
	res, err := http.Get(srv.URL + "/a/" + id + "/")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	page := string(raw)
	if !strings.Contains(page, `<meta property="og:image" content="http://share.test/a/`+id+`/_cover.svg">`) || !strings.Contains(page, `og:title" content="OG title"`) {
		t.Fatalf("%s", page)
	}
}

func config64() config.Limits { return config.Limits{VersionBytes: 1 << 20, Files: 50} }
