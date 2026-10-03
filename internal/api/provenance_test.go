package api

import "testing"

func TestVersionProvenance(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<p>one</p>"}}
	code, b := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"agent": "claude-code", "model": "claude-opus-5-5"}, files))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	id := b["id"].(string)
	files[0].Body = "<p>two</p>"
	do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, nil, files))

	_, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/versions", tok))
	vs := list["versions"].([]any)
	v1, v2 := vs[0].(map[string]any), vs[1].(map[string]any)
	if v1["agent"] != "claude-code" || v1["model"] != "claude-opus-5-5" || v1["key"] == nil {
		t.Fatalf("v1: %v", v1)
	}
	if v2["agent"] != "" || v2["model"] != "" {
		t.Fatalf("v2 should carry nothing it was not told: %v", v2)
	}
	if code, _ := do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"agent": "bad\nname"}, files)); code != 400 {
		t.Fatalf("control character accepted: %d", code)
	}
}
