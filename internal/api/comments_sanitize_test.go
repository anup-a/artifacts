package api

import (
	"encoding/json"
	"testing"
)

// publishOne publishes a one-file artifact and returns its id.
func publishOne(t *testing.T, srvURL, tok, body string) string {
	t.Helper()
	code, b := do(t, publishReq(t, "POST", srvURL+"/api/artifacts", tok, "", []pubFile{{"index.html", body}}, nil))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	return b["id"].(string)
}

// threadJSON encodes a viewer create-thread payload, so control characters
// reach the server as proper JSON escapes rather than raw bytes.
func threadJSON(t *testing.T, name, body string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"author_name": name,
		"body":        body,
		"anchor":      json.RawMessage(anchor),
		"version":     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func firstBody(th map[string]any) string {
	cs, ok := th["comments"].([]any)
	if !ok || len(cs) == 0 {
		return ""
	}
	s, _ := cs[0].(map[string]any)["body"].(string)
	return s
}

const nul = "\x00"

func TestCommentBodyStripsHTMLTags(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "<b>x</b>"), nil))
	if code != 201 {
		t.Fatalf("%d %v", code, th)
	}
	if got := firstBody(th); got != "x" {
		t.Fatalf("body = %q, want %q (tags must be stripped)", got, "x")
	}
}

func TestCommentNameStripsHTMLTags(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "<script>alert(1)</script>Ann", "hi"), nil))
	if code != 201 {
		t.Fatalf("%d %v", code, th)
	}
	name := th["comments"].([]any)[0].(map[string]any)["author_name"].(string)
	if name != "alert(1)Ann" {
		t.Fatalf("author_name = %q, want %q", name, "alert(1)Ann")
	}
}

func TestCommentBodyRejectsControlCharacters(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "bad"+nul+"body"), nil))
	if code != 400 {
		t.Fatalf("body with NUL: got %d, want 400", code)
	}

	// Newlines and tabs stay legal in a body.
	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "line1\nline2\tend"), nil))
	if code != 201 {
		t.Fatalf("body with newline/tab: got %d %v, want 201", code, th)
	}
	if got := firstBody(th); got != "line1\nline2\tend" {
		t.Fatalf("body = %q, newline/tab must be preserved", got)
	}
}

func TestCommentNameRejectsControlCharacters(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	// newline, NUL and tab are all illegal in a display name.
	for _, name := range []string{"An\nn", "An" + nul + "n", "An\tn"} {
		code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, name, "hi"), nil))
		if code != 400 {
			t.Fatalf("author_name %q: got %d, want 400", name, code)
		}
	}
}

func TestCommentBodyRejectsTagOnlyBody(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")

	// Stripping runs before the length check, so a tags-only body is empty -> 400.
	code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "<br/>"), nil))
	if code != 400 {
		t.Fatalf("tag-only body: got %d, want 400", code)
	}
}
