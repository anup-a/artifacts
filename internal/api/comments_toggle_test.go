package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// publishWith is publishReq with extra manifest fields, for the comments switch.
func publishWith(t *testing.T, method, url, token string, extra map[string]any, files []pubFile) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	var mf []map[string]any
	for _, f := range files {
		mf = append(mf, map[string]any{"path": f.Path, "sha256": sha(f.Body), "size": len(f.Body)})
	}
	man := map[string]any{"files": mf}
	for k, v := range extra {
		man[k] = v
	}
	raw, _ := json.Marshal(man)
	p, _ := mw.CreateFormField("manifest")
	p.Write(raw)
	for _, f := range files {
		fw, _ := mw.CreateFormFile(sha(f.Body), f.Path)
		fw.Write([]byte(f.Body))
	}
	mw.Close()
	req, _ := http.NewRequest(method, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func pageBody(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func TestCommentsSwitchViaManifest(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	page := []pubFile{{"index.html", "<p>hello world</p>"}}
	newThread := `{"author_name":"Ann","body":"typo","anchor":` + anchor + `,"version":1}`

	code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", page, nil))
	if code != 201 || b["comments_enabled"] != true {
		t.Fatalf("default must be on: %d %v", code, b)
	}
	id := b["id"].(string)
	code, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", newThread, nil))
	if code != 201 {
		t.Fatalf("viewer thread while on: %d %v", code, th)
	}
	tid := th["id"].(string)
	if !strings.Contains(pageBody(t, srv.URL+"/a/"+id+"/"), "/_rt/comments.js") {
		t.Fatal("runtime missing while comments are on")
	}

	// Switch off through PUT.
	code, b = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"comments_enabled": false}, page))
	if code != 200 || b["comments_enabled"] != false || b["version"].(float64) != 2 {
		t.Fatalf("put off: %d %v", code, b)
	}
	code, b = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok))
	if code != 200 || b["comments_enabled"] != false {
		t.Fatalf("get: %d %v", code, b)
	}
	_, b = do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok))
	if b["artifacts"].([]any)[0].(map[string]any)["comments_enabled"] != false {
		t.Fatalf("list: %v", b)
	}
	// The runtime stays for page state, told to draw no panel.
	if body := pageBody(t, srv.URL+"/a/"+id+"/"); !strings.Contains(body, `data-comments="off"`) || !strings.Contains(body, "hello world") {
		t.Fatalf("page with comments off: %s", body)
	}

	// Every viewer route is a 404 now.
	viewer := []*http.Request{
		jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", newThread, nil),
		jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments", "", nil),
		jsonReq(t, "GET", srv.URL+"/c/"+id+"/meta", "", nil),
		jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/"+tid+"/reply", `{"author_name":"Bob","body":"+1"}`, nil),
		jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments/"+tid+"/seen", `{"version":2,"anchor_status":"attached"}`, nil),
	}
	for _, req := range viewer {
		code, body := do(t, req)
		if code != 404 || body["error"].(map[string]any)["code"] != "NOT_FOUND" {
			t.Fatalf("%s %s: %d %v", req.Method, req.URL.Path, code, body)
		}
	}

	// The owner still sees, answers and resolves the old thread.
	code, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments", tok))
	if code != 200 || len(list["threads"].([]any)) != 1 {
		t.Fatalf("owner list: %d %v", code, list)
	}
	if code, _ = do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/reply", `{"body":"noted"}`, map[string]string{"Authorization": "Bearer " + tok})); code != 201 {
		t.Fatalf("owner reply: %d", code)
	}
	if code, _ = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/comments/"+tid+"/resolve", tok)); code != 200 {
		t.Fatalf("owner resolve: %d", code)
	}

	// A PUT without the field leaves the switch alone; with true it comes back.
	code, b = do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", page, nil))
	if code != 200 || b["comments_enabled"] != false {
		t.Fatalf("put without field: %d %v", code, b)
	}
	code, b = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"comments_enabled": true}, page))
	if code != 200 || b["comments_enabled"] != true {
		t.Fatalf("put on: %d %v", code, b)
	}
	if code, _ = do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/meta", "", nil)); code != 200 {
		t.Fatalf("meta after re-enable: %d", code)
	}
	if !strings.Contains(pageBody(t, srv.URL+"/a/"+id+"/"), "/_rt/comments.js") {
		t.Fatal("runtime not restored")
	}

	// A new artifact can start with comments off.
	code, b = do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"comments_enabled": false}, page))
	if code != 201 || b["comments_enabled"] != false {
		t.Fatalf("create off: %d %v", code, b)
	}
	if code, _ = do(t, jsonReq(t, "GET", srv.URL+"/c/"+b["id"].(string)+"/comments", "", nil)); code != 404 {
		t.Fatalf("viewer list on new off artifact: %d", code)
	}
}
