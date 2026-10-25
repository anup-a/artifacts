package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func linkReq(t *testing.T, srvURL, tok, body string) *http.Request {
	t.Helper()
	req, _ := http.NewRequest("POST", srvURL+"/api/links", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// A link is registered, refreshed rather than duplicated, listed apart from
// hosted pages, and deleted by id or by URL, all within one workspace.
func TestLinksAPI(t *testing.T) {
	srv, d, tok := newTestServer(t, false)

	req := linkReq(t, srv.URL, tok, `{"url":"https://My-App.vercel.app#top","title":"Launch page","project":"creatica"}`)
	req.Header.Set(ClientHeader, "claude-code")
	code, b := do(t, req)
	if code != 201 || b["url"] != "https://my-app.vercel.app/" || b["provider"] != "Vercel" || b["agent"] != "claude-code" || b["hosted"] != false || b["created"] != true {
		t.Fatalf("create: %d %v", code, b)
	}
	id := b["id"].(string)
	if !strings.HasPrefix(id, "ln_") {
		t.Fatalf("id: %q", id)
	}

	// The hook registers again on every deploy, with no title: same link.
	code, b = do(t, linkReq(t, srv.URL, tok, `{"url":"https://my-app.vercel.app/","agent":"grok"}`))
	if code != 200 || b["id"] != id || b["title"] != "Launch page" || b["project"] != "creatica" || b["agent"] != "grok" || b["created"] != false {
		t.Fatalf("refresh: %d %v", code, b)
	}

	for _, bad := range []string{`{"url":"http://localhost:3000"}`, `{"url":"https://abc.share.test/"}`, `{"url":"ftp://x.com"}`, `not json`, `{"url":"https://x.com","agent":"` + strings.Repeat("a", 65) + `"}`} {
		if code, b := do(t, linkReq(t, srv.URL, tok, bad)); code != 400 {
			t.Fatalf("%s: %d %v", bad, code, b)
		}
	}

	code, b = do(t, authReq(t, "GET", srv.URL+"/api/links", tok))
	if ls, _ := b["links"].([]any); code != 200 || len(ls) != 1 {
		t.Fatalf("list: %d %v", code, b)
	}
	// Links are not pages: the artifact list does not see them.
	if _, b := do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok)); len(b["artifacts"].([]any)) != 0 {
		t.Fatalf("a link showed up as an artifact: %v", b)
	}

	other := mintKey(t, d, "personal")
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/links/"+id, other)); code != 404 {
		t.Fatalf("another workspace deleted it: %d", code)
	}
	if _, b := do(t, authReq(t, "GET", srv.URL+"/api/links", other)); len(b["links"].([]any)) != 0 {
		t.Fatalf("another workspace sees it: %v", b)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/links/"+url.PathEscape("https://my-app.vercel.app/"), tok)); code != 204 {
		t.Fatalf("delete by url: %d", code)
	}
	if code, _ := do(t, authReq(t, "DELETE", srv.URL+"/api/links/"+id, tok)); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestLinksRefuseGuests(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	g := newGuest(t, srv.URL)
	if code, b := do(t, linkReq(t, srv.URL, g["token"].(string), `{"url":"https://a.vercel.app"}`)); code != 403 {
		t.Fatalf("guest link: %d %v", code, b)
	}
}
