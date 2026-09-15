package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

type pubFile struct{ Path, Body string }

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// publishReq builds a multipart publish request including the given blobs (nil upload = all).
func publishReq(t *testing.T, method, url, token, title string, files []pubFile, upload []string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	var mf []map[string]any
	for _, f := range files {
		mf = append(mf, map[string]any{"path": f.Path, "sha256": sha(f.Body), "size": len(f.Body)})
	}
	man, _ := json.Marshal(map[string]any{"title": title, "files": mf})
	p, _ := mw.CreateFormField("manifest")
	p.Write(man)
	for _, f := range files {
		if upload != nil && !contains(upload, sha(f.Body)) {
			continue
		}
		fw, _ := mw.CreateFormFile(sha(f.Body), f.Path)
		fw.Write([]byte(f.Body))
	}
	mw.Close()
	req, _ := http.NewRequest(method, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func do(t *testing.T, req *http.Request) (int, map[string]any) {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	raw, _ := io.ReadAll(res.Body)
	json.Unmarshal(raw, &body)
	return res.StatusCode, body
}

func TestPublishCreateAndUpdate(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<html><body>v1</body></html>"}, {"a.css", "body{}"}}
	code, body := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Hello", files, nil))
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	id := body["id"].(string)
	if len(id) != 12 || body["url"] != "http://share.test/a/"+id+"/" || body["version"].(float64) != 1 {
		t.Fatalf("%v", body)
	}
	// preflight: only index changed
	files[0].Body = "<html><body>v2</body></html>"
	pf, _ := json.Marshal(map[string]any{"files": []map[string]any{{"path": "index.html", "sha256": sha(files[0].Body), "size": len(files[0].Body)}, {"path": "a.css", "sha256": sha("body{}"), "size": 6}}})
	req, _ := http.NewRequest("POST", srv.URL+"/api/artifacts/preflight", bytes.NewReader(pf))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	code, body = do(t, req)
	missing := body["missing"].([]any)
	if code != 200 || len(missing) != 1 || missing[0] != sha(files[0].Body) {
		t.Fatalf("preflight %d %v", code, body)
	}
	// update uploading only the missing blob
	code, body = do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "Hello", files, []string{sha(files[0].Body)}))
	if code != 200 || body["version"].(float64) != 2 {
		t.Fatalf("update: %d %v", code, body)
	}
}

func TestPublishErrors(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	// missing blob part
	files := []pubFile{{"index.html", "x"}}
	code, body := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", files, []string{}))
	if code != 400 || !strings.Contains(fmt.Sprint(body), "missing blob") {
		t.Fatalf("%d %v", code, body)
	}
	// checksum mismatch: part named sha(x) but content y
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	man, _ := json.Marshal(map[string]any{"files": []map[string]any{{"path": "index.html", "sha256": sha("x"), "size": 1}}})
	p, _ := mw.CreateFormField("manifest")
	p.Write(man)
	fw, _ := mw.CreateFormFile(sha("x"), "index.html")
	fw.Write([]byte("y"))
	mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/artifacts", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	code, body = do(t, req)
	if code != 400 || !strings.Contains(fmt.Sprint(body), "checksum") {
		t.Fatalf("%d %v", code, body)
	}
	// update unknown id
	code, _ = do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/nope00000000", tok, "", []pubFile{{"index.html", "x"}}, nil))
	if code != 404 {
		t.Fatalf("unknown id: %d", code)
	}
}

func TestPublishQuota(t *testing.T) {
	srv, _, tok := newTestServer(t, false) // ArtifactsPerKey = 5 in test cfg
	for i := range 5 {
		code, _ := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", fmt.Sprint("n", i)}}, nil))
		if code != 201 {
			t.Fatalf("publish %d: %d", i, code)
		}
	}
	code, body := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "n6"}}, nil))
	if code != 429 || body["error"].(map[string]any)["code"] != "QUOTA_EXCEEDED" {
		t.Fatalf("%d %v", code, body)
	}
}
