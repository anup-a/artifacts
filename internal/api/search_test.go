package api

import "testing"

// The dashboard has searched titles since the gallery shipped; the store
// filter was simply never reachable over the API, so an agent holding a key
// could page the shelf but not look anything up on it.
func TestListFiltersByTitleQuery(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, spec := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Rebooking policy line", []pubFile{{"index.html", "a"}}, nil))
	_, notes := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Week 38 metrics", []pubFile{{"index.html", "b"}}, nil))

	ids := func(url string) []string {
		t.Helper()
		code, body := do(t, authReq(t, "GET", url, tok))
		if code != 200 {
			t.Fatalf("%s: %d %v", url, code, body)
		}
		out := []string{}
		for _, a := range body["artifacts"].([]any) {
			out = append(out, a.(map[string]any)["id"].(string))
		}
		return out
	}

	// Case-insensitive substring of the title, not a prefix match.
	if got := ids(srv.URL + "/api/artifacts?q=policy"); len(got) != 1 || got[0] != spec["id"] {
		t.Fatalf("q=policy: %v", got)
	}
	if got := ids(srv.URL + "/api/artifacts?q=WEEK"); len(got) != 1 || got[0] != notes["id"] {
		t.Fatalf("q=WEEK: %v", got)
	}
	if got := ids(srv.URL + "/api/artifacts?q=nothing+here"); len(got) != 0 {
		t.Fatalf("no match should be empty: %v", got)
	}
	if got := ids(srv.URL + "/api/artifacts"); len(got) != 2 {
		t.Fatalf("no q should list everything: %v", got)
	}
	// A wildcard is a character to search for, not a pattern to run.
	if got := ids(srv.URL + "/api/artifacts?q=%25"); len(got) != 0 {
		t.Fatalf("percent should not match everything: %v", got)
	}
}
