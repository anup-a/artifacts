package api

import "testing"

func TestSuggestedEdit(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	post := func(body string) (int, map[string]any) {
		return do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", body, nil))
	}
	code, th := post(`{"author_name":"Ann","body":"typo","anchor":` + anchor + `,"version":1,"suggestion":"hello, world"}`)
	if code != 201 || th["suggestion"] != "hello, world" {
		t.Fatalf("%d %v", code, th)
	}
	_, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id+"/comments", tok))
	if got := list["threads"].([]any)[0].(map[string]any)["suggestion"]; got != "hello, world" {
		t.Fatalf("owner sees %v", got)
	}
	for name, body := range map[string]string{
		"unchanged":   `{"author_name":"Ann","body":"x","anchor":` + anchor + `,"version":1,"suggestion":"hello"}`,
		"element":     `{"author_name":"Ann","body":"x","anchor":{"kind":"element","css":"p","text":"hello"},"version":1,"suggestion":"bye"}`,
		"only markup": `{"author_name":"Ann","body":"x","anchor":` + anchor + `,"version":1,"suggestion":"<b></b>"}`,
	} {
		if code, b := post(body); code != 400 {
			t.Errorf("%s: %d %v", name, code, b)
		}
	}
	// A plain comment still has an empty suggestion.
	if code, th := post(`{"author_name":"Ann","body":"plain","anchor":` + anchor + `,"version":1}`); code != 201 || th["suggestion"] != "" {
		t.Fatalf("plain: %d %v", code, th)
	}
}
