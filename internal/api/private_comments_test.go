package api

import (
	"context"
	"testing"
)

func TestPrivateArtifactsDenyAllViewerCommentRoutes(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>private later</p>")
	code, thread := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", `{"author_name":"A","body":"secret thread","anchor":`+anchor+`,"version":1}`, nil))
	if code != 201 {
		t.Fatalf("thread: %d %v", code, thread)
	}
	tid := thread["id"].(string)
	if err := d.Store.SetVisibility(context.Background(), id, "private"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/meta", ""}, {"GET", "/comments", ""},
		{"POST", "/comments", `{"author_name":"A","body":"secret thread","anchor":` + anchor + `,"version":1}`},
		{"POST", "/comments/" + tid + "/reply", `{"author_name":"A","body":"reply"}`},
		{"POST", "/comments/" + tid + "/seen", `{"version":1,"anchor_status":"attached"}`},
	} {
		code, b := do(t, jsonReq(t, tc.method, srv.URL+"/c/"+id+tc.path, tc.body, nil))
		if code != 404 {
			t.Fatalf("viewer %s %s: %d %v", tc.method, tc.path, code, b)
		}
	}
	if code, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments", tok)); code != 200 {
		t.Fatalf("owner lost comments: %d %v", code, b)
	}
}
