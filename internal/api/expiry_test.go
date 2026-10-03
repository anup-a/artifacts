package api

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestArtifactExpiry(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	url := srv.URL + "/api/artifacts/" + id + "/expiry"
	auth := map[string]string{"Authorization": "Bearer " + tok}

	if code, _ := do(t, jsonReq(t, "POST", url, `{"expires_at":"2001-01-01T00:00:00Z"}`, auth)); code != 400 {
		t.Fatalf("past expiry accepted: %d", code)
	}
	when := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	code, v := do(t, jsonReq(t, "POST", url, `{"expires_at":"`+when+`"}`, auth))
	if code != 200 || v["expires_at"] == nil {
		t.Fatalf("set: %d %v", code, v)
	}
	code, v = do(t, jsonReq(t, "POST", url, `{"expires_at":null}`, auth))
	if code != 200 || v["expires_at"] != nil {
		t.Fatalf("clear: %d %v", code, v)
	}
	// Once past, the page is gone, and the GC erases it.
	past := time.Now().Add(-time.Minute)
	if err := d.Store.SetExpiry(context.Background(), id, &past); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(srv.URL + "/a/" + id + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 410 {
		t.Fatalf("expired page: %d", res.StatusCode)
	}
	if err := gcOnce(context.Background(), d, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	erased(t, d, id)
}

func TestGuestCannotMoveExpiry(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	tok := newGuest(t, srv.URL)["token"].(string)
	id := publishOne(t, srv.URL, tok, "<p>x</p>")
	if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/expiry", `{"expires_at":null}`, map[string]string{"Authorization": "Bearer " + tok})); code != 403 {
		t.Fatalf("guest cleared expiry: %d", code)
	}
}

func TestShareCodeExpiry(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	_, b := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"visibility": "private"}, []pubFile{{"index.html", "<p>x</p>"}}))
	id := b["id"].(string)
	when := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	code, s := do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/share-code", `{"action":"issue","expires_at":"`+when+`"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 200 || s["share_code"] == nil || s["share_code_expires_at"] == nil {
		t.Fatalf("issue: %d %v", code, s)
	}
	// Past its end the code reads as gone everywhere.
	past := time.Now().Add(-time.Minute)
	if err := d.Store.SetShareCode(context.Background(), id, s["share_code"].(string), &past); err != nil {
		t.Fatal(err)
	}
	a, err := d.Store.Artifact(context.Background(), id)
	if err != nil || a.ShareCode != "" {
		t.Fatalf("expired code still readable: %q %v", a.ShareCode, err)
	}
	res, err := http.Get(srv.URL + "/a/" + id + "/?k=" + s["share_code"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatal("expired code still opens the page")
	}
}
