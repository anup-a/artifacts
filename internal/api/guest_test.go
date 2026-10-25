package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/guest"
)

// newGuestServer turns guest publishing on and returns a clock the test can
// move forward.
func newGuestServer(t *testing.T) (*httptest.Server, Deps, *atomic.Int64) {
	t.Helper()
	var offset atomic.Int64
	srv, d, _ := newTestServerWith(t, true, func(d *Deps) {
		d.Cfg.GuestPublish = true
		d.Now = func() time.Time { return time.Now().UTC().Add(time.Duration(offset.Load())) }
	})
	return srv, d, &offset
}

func newGuest(t *testing.T, srvURL string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("POST", srvURL+"/api/guest", nil)
	code, b := do(t, req)
	if code != 201 {
		t.Fatalf("guest: %d %v", code, b)
	}
	return b
}

func TestGuestOffByDefault(t *testing.T) {
	srv, _, _ := newTestServer(t, true)
	req, _ := http.NewRequest("POST", srv.URL+"/api/guest", nil)
	if code, _ := do(t, req); code != 404 {
		t.Fatalf("got %d", code)
	}
}

func TestGuestPublishLimitsAndMe(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	g := newGuest(t, srv.URL)
	tok := g["token"].(string)
	if !strings.HasPrefix(tok, "ak_") || g["guest"] != true || g["api"] != "http://api.test" ||
		!strings.HasPrefix(g["claim_url"].(string), "http://api.test/app/claim/t_") {
		t.Fatalf("%v", g)
	}
	exp, err := time.Parse(time.RFC3339, g["expires_at"].(string))
	if err != nil || time.Until(exp) < 23*time.Hour || time.Until(exp) > 25*time.Hour {
		t.Fatalf("expires_at %v", g["expires_at"])
	}

	code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Try", []pubFile{{"index.html", "<body>hi</body>"}}, nil))
	if code != 201 || b["guest"] != true || b["expires_at"] != g["expires_at"] || b["claim_url"] != g["claim_url"] {
		t.Fatalf("publish: %d %v", code, b)
	}
	if page := pageBody(t, srv.URL+"/a/"+b["id"].(string)+"/"); !strings.Contains(page, "Temporary guest page") || strings.Contains(page, "Published with byagent") {
		t.Fatalf("want the guest badge and not the credit: %s", page)
	}

	code, me := do(t, authReq(t, "GET", srv.URL+"/api/me", tok))
	if code != 200 || me["guest"] != true || me["tenant"].(map[string]any)["plan"] != "guest" {
		t.Fatalf("me: %d %v", code, me)
	}

	priv := publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"visibility": "private"}, []pubFile{{"index.html", "<p>x</p>"}})
	if code, b := do(t, priv); code != 403 {
		t.Fatalf("private: %d %v", code, b)
	}
	for i := 0; i < guest.Artifacts-1; i++ {
		if code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>" + string(rune('a'+i)) + "</p>"}}, nil)); code != 201 {
			t.Fatalf("publish %d: %d %v", i, code, b)
		}
	}
	if code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>over</p>"}}, nil)); code != 429 || b["error"].(map[string]any)["code"] != "QUOTA_EXCEEDED" {
		t.Fatalf("quota: %d %v", code, b)
	}
}

func TestGuestKeyAndPagesExpire(t *testing.T) {
	srv, d, offset := newGuestServer(t)
	tok := newGuest(t, srv.URL)["token"].(string)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>x</p>"}}, nil))
	url := srv.URL + "/a/" + b["id"].(string) + "/"

	offset.Store(int64(guest.TTL + time.Minute))
	code, e := do(t, authReq(t, "GET", srv.URL+"/api/me", tok))
	if code != 401 || !strings.Contains(e["error"].(map[string]any)["message"].(string), "guest key expired") {
		t.Fatalf("expired key: %d %v", code, e)
	}
	if err := gcOnce(context.Background(), d, d.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 410 {
		t.Fatalf("expired page: %d", res.StatusCode)
	}
}

func TestGuestCreationIsRateLimited(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	for i := 0; i < 5; i++ {
		newGuest(t, srv.URL)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/guest", nil)
	if code, _ := do(t, req); code != 429 {
		t.Fatalf("got %d", code)
	}
}

func TestGuestPageCapsOpenThreads(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	tok := newGuest(t, srv.URL)["token"].(string)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	for i := 0; i < guest.OpenThreads; i++ {
		if code, b := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "note"), nil)); code != 201 {
			t.Fatalf("thread %d: %d %v", i, code, b)
		}
	}
	if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "one more"), nil)); code != 429 {
		t.Fatalf("got %d", code)
	}
}
