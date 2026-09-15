package api

import (
	"net/http"
	"strings"
	"testing"
)

func jsonReq(t *testing.T, method, url, body string, hdr map[string]string) *http.Request {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

const anchor = `{"kind":"text","quote":"hello","prefix":"","suffix":" world"}`

func TestViewerAndOwnerCommentFlow(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>hello world</p>"}}, nil))
	id := b["id"].(string)

	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", `{"author_name":" Ann ","body":"typo here","anchor":`+anchor+`,"version":1}`, map[string]string{"Idempotency-Key": "k1"}))
	if code != 201 || th["status"] != "open" || th["anchor_status"] != "unknown" {
		t.Fatalf("%d %v", code, th)
	}
	tid := th["id"].(string)
	if !strings.HasPrefix(tid, "th_") || th["comments"].([]any)[0].(map[string]any)["author_name"] != "Ann" {
		t.Fatalf("%v", th)
	}
	// idempotent replay
	code, th2 := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", `{"author_name":"Ann","body":"typo here","anchor":`+anchor+`,"version":1}`, map[string]string{"Idempotency-Key": "k1"}))
	if code != 201 || th2["id"] != tid {
		t.Fatalf("idempotency: %d %v", code, th2)
	}
	// viewer reply
	code, c := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/"+tid+"/reply", `{"author_name":"Bob","body":"+1"}`, nil))
	if code != 201 || !strings.HasPrefix(c["id"].(string), "cm_") {
		t.Fatalf("%d %v", code, c)
	}
	// seen beacon
	code, _ = do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/"+tid+"/seen", `{"version":1,"anchor_status":"attached"}`, nil))
	if code != 204 {
		t.Fatal(code)
	}
	// owner list
	code, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments", tok))
	threads := list["threads"].([]any)
	if code != 200 || len(threads) != 1 || threads[0].(map[string]any)["anchor_status"] != "attached" || len(threads[0].(map[string]any)["comments"].([]any)) != 2 {
		t.Fatalf("%d %v", code, list)
	}
	// owner reply + resolve
	code, c = do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/reply", `{"body":"fixed in v2"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 201 || c["author_kind"] != "owner" || c["author_name"] != "Author" {
		t.Fatalf("%d %v", code, c)
	}
	code, th = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/resolve", tok))
	if code != 200 || th["status"] != "resolved" {
		t.Fatalf("%d %v", code, th)
	}
	// viewer default list hides resolved; status=all shows it
	_, v := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments", "", nil))
	if len(v["threads"].([]any)) != 0 {
		t.Fatalf("resolved should be hidden: %v", v)
	}
	_, v = do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments?status=all", "", nil))
	if len(v["threads"].([]any)) != 1 {
		t.Fatalf("%v", v)
	}
	// owner list open → empty; status=resolved → 1
	_, list = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments?status=resolved", tok))
	if len(list["threads"].([]any)) != 1 {
		t.Fatalf("%v", list)
	}
}

func TestViewerValidation(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "x"}}, nil))
	id := b["id"].(string)
	cases := []string{
		`{"author_name":"","body":"x","anchor":` + anchor + `,"version":1}`,
		`{"author_name":"A","body":"   ","anchor":` + anchor + `,"version":1}`,
		`{"author_name":"A","body":"x","anchor":{"kind":"video"},"version":1}`,
		`{"author_name":"A","body":"x","anchor":{"kind":"text"},"version":1}`,
		`{"author_name":"A","body":"x","anchor":` + anchor + `,"version":9}`,
		`{"author_name":"A","body":"` + strings.Repeat("y", 300) + `","anchor":` + anchor + `,"version":1}`,
	}
	for i, body := range cases {
		if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", body, nil)); code != 400 {
			t.Errorf("case %d: %d", i, code)
		}
	}
	if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/nope00000000/comments", cases[0], nil)); code != 404 {
		t.Errorf("unknown artifact: %d", code)
	}
	if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/th_missing000/reply", `{"author_name":"A","body":"x"}`, nil)); code != 404 {
		t.Errorf("unknown thread: %d", code)
	}
}

func TestCommentRateLimit(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "x"}}, nil))
	id := b["id"].(string)
	// limit is 100 in test cfg; 100 replies to an unknown thread all 404 before the
	// rate limit trips, then the 101st is rejected — the limiter is checked before
	// the thread lookup.
	for i := range 100 {
		if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/th_x/reply", `{"author_name":"A","body":"x"}`, nil)); code != 404 {
			t.Fatalf("call %d: %d", i, code)
		}
	}
	code, body := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/th_x/reply", `{"author_name":"A","body":"x"}`, nil))
	if code != 429 || body["error"].(map[string]any)["code"] != "RATE_LIMITED" {
		t.Fatalf("%d %v (limit applies before thread lookup)", code, body)
	}
}
