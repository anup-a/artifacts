package api

import (
	"testing"
)

func TestThreadProgress(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	_, th := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "typo"), nil))
	tid := th["id"].(string)
	if th["progress"] != "" {
		t.Fatalf("new thread progress %v", th["progress"])
	}
	url := srv.URL + "/api/artifacts/" + id + "/comments/" + tid + "/progress"
	auth := map[string]string{"Authorization": "Bearer " + tok}

	code, v := do(t, jsonReq(t, "POST", url, `{"progress":"working"}`, auth))
	if code != 200 || v["progress"] != "working" || v["status"] != "open" || len(v["comments"].([]any)) != 1 {
		t.Fatalf("working: %d %v", code, v)
	}
	// Readers see it too.
	_, list := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/comments", "", nil))
	if got := list["threads"].([]any)[0].(map[string]any)["progress"]; got != "working" {
		t.Fatalf("viewer sees %v", got)
	}
	if code, _ := do(t, jsonReq(t, "POST", url, `{"progress":"declined"}`, auth)); code != 400 {
		t.Fatalf("decline without note: %d", code)
	}
	if code, _ := do(t, jsonReq(t, "POST", url, `{"progress":"done"}`, auth)); code != 400 {
		t.Fatalf("unknown progress: %d", code)
	}
	code, v = do(t, jsonReq(t, "POST", url, `{"progress":"declined","note":"Keeping it: the spelling is British."}`, auth))
	cs := v["comments"].([]any)
	if code != 200 || v["progress"] != "declined" || v["status"] != "resolved" || len(cs) != 2 || cs[1].(map[string]any)["author_kind"] != "owner" {
		t.Fatalf("declined: %d %v", code, v)
	}
}
