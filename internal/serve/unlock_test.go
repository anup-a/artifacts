package serve

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

// codeFixture is a private artifact shared by the code "481523".
func codeFixture(t *testing.T) (*http.ServeMux, *Handler, string) {
	t.Helper()
	_, st, id := fixture(t)
	ctx := t.Context()
	if err := st.SetVisibility(ctx, id, "private"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareCode(ctx, id, "481523", nil); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"),
		ShareOrigin: "http://share.test", APIOrigin: "http://app.test", UnlockSecret: "unlock-test-secret",
		Gate: func(w http.ResponseWriter, r *http.Request, g GateView) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			switch {
			case g.Locked:
				w.WriteHeader(429)
				_, _ = w.Write([]byte("too many attempts"))
			case g.Wrong:
				w.WriteHeader(403)
				_, _ = w.Write([]byte("wrong code"))
			default:
				_, _ = w.Write([]byte("enter the code"))
			}
		}}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, h, id
}

func do(mux *http.ServeMux, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func fetchCode(mux *http.ServeMux, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return do(mux, req)
}

func postCode(mux *http.ServeMux, id, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://share.test/a/"+id+"/_unlock",
		strings.NewReader(url.Values{"code": {code}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(mux, req)
}

func unlockCookieOf(t *testing.T, res *httptest.ResponseRecorder, id string) *http.Cookie {
	t.Helper()
	for _, c := range res.Result().Cookies() {
		if c.Name == unlockCookieName(id) && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no unlock cookie in %v", res.Result().Cookies())
	return nil
}

func TestShareCodeOpensByLinkAndByForm(t *testing.T) {
	mux, h, id := codeFixture(t)

	// Nobody holding nothing gets the gate, not the page and not a bare 404.
	res := fetchCode(mux, "http://share.test/a/"+id+"/")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "enter the code") {
		t.Fatalf("gate: %d %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("gate cache %v", res.Header())
	}

	// Door one: the code on the link. It must redirect rather than render, so
	// the address the viewer keeps does not carry the code.
	res = fetchCode(mux, "http://share.test/a/"+id+"/?k=481523")
	if res.Code != 303 || res.Header().Get("Location") != "/a/"+id+"/" {
		t.Fatalf("link unlock: %d %v", res.Code, res.Header())
	}
	linkCookie := unlockCookieOf(t, res, id)
	if strings.Contains(linkCookie.Value, "481523") {
		t.Fatal("cookie carries the code itself")
	}
	// The unlock has to reach both places this artifact answers a code holder:
	// its pages, and its viewer comment routes. Scoping it to the pages alone
	// leaves a reader who can see the artifact but not comment on it.
	paths := map[string]bool{}
	for _, c := range res.Result().Cookies() {
		if c.Name == unlockCookieName(id) {
			if !c.HttpOnly {
				t.Fatalf("unlock cookie readable from script: %+v", c)
			}
			paths[c.Path] = true
		}
	}
	for _, want := range unlockCookiePaths(id) {
		if !paths[want] {
			t.Fatalf("no unlock cookie for %s, got %v", want, paths)
		}
	}
	if paths["/"] {
		t.Fatal("unlock cookie sent to the whole origin")
	}

	// Door two: the code typed into the form, reaching the same cookie.
	res = postCode(mux, id, "481523")
	if res.Code != 303 {
		t.Fatalf("form unlock: %d", res.Code)
	}
	formCookie := unlockCookieOf(t, res, id)
	if formCookie.Value != linkCookie.Value {
		t.Fatal("the two doors mint different unlocks")
	}

	// With the cookie the page and its assets serve, and the comment runtime
	// is injected: a code holder is a viewer, not a spectator.
	for _, path := range []string{"/a/" + id + "/", "/a/" + id + "/v/1/s/a.css", "/a/" + id + "/v/1/", "/a/" + id + "/_cover.svg"} {
		res := fetchCode(mux, "http://share.test"+path, linkCookie)
		if res.Code != 200 {
			t.Fatalf("unlocked %s: %d %s", path, res.Code, res.Body.String())
		}
	}
	if body := fetchCode(mux, "http://share.test/a/"+id+"/", linkCookie).Body.String(); !strings.Contains(body, "data-artifact") {
		t.Fatalf("code holder got no comment runtime: %s", body)
	}
	// Nothing about an unlocked private page may be publicly cacheable.
	if cc := fetchCode(mux, "http://share.test/a/"+id+"/", linkCookie).Header().Get("Cache-Control"); strings.Contains(cc, "public") {
		t.Fatalf("cache %q", cc)
	}
	_ = h
}

// Following the link again, already unlocked, must still strip the code:
// otherwise it settles into the address bar and history of a browser that
// had no further use for it.
func TestShareCodeLinkAlwaysLeavesTheURL(t *testing.T) {
	mux, _, id := codeFixture(t)
	cookie := unlockCookieOf(t, fetchCode(mux, "http://share.test/a/"+id+"/?k=481523"), id)
	res := fetchCode(mux, "http://share.test/a/"+id+"/?k=481523", cookie)
	if res.Code != 303 || res.Header().Get("Location") != "/a/"+id+"/" {
		t.Fatalf("already unlocked: %d %v", res.Code, res.Header())
	}
	// An artifact with no code owns its own query string; "k" there is the
	// published page's business and must be left alone.
	_, st, plain := fixture(t)
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"), ShareOrigin: "http://share.test"}
	plainMux := http.NewServeMux()
	h.Register(plainMux)
	if res := fetchCode(plainMux, "http://share.test/a/"+plain+"/?k=anything"); res.Code != 200 {
		t.Fatalf("public page with its own k param: %d", res.Code)
	}
}

func TestShareCodeIsScopedAndRevocable(t *testing.T) {
	mux, h, id := codeFixture(t)
	cookie := unlockCookieOf(t, fetchCode(mux, "http://share.test/a/"+id+"/?k=481523"), id)

	// The cookie is signed over the artifact id, so replaying it at another
	// artifact opens nothing even before Path would have stopped the browser.
	other := &http.Cookie{Name: unlockCookieName("someotherid00"), Value: cookie.Value}
	if res := fetchCode(mux, "http://share.test/a/someotherid00/", other); res.Code == 200 {
		t.Fatal("an unlock for one artifact opened another")
	}

	// Rotating the code revokes the old one: the live cookie stops working,
	// the old link stops working, and the viewer is asked again rather than
	// refused outright.
	fresh := ids.NewShareCode()
	if err := h.Store.SetShareCode(t.Context(), id, fresh, nil); err != nil {
		t.Fatal(err)
	}
	res := fetchCode(mux, "http://share.test/a/"+id+"/", cookie)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "enter the code") {
		t.Fatalf("rotated: %d %s", res.Code, res.Body.String())
	}
	if res := fetchCode(mux, "http://share.test/a/"+id+"/?k=481523"); !strings.Contains(res.Body.String(), "wrong code") {
		t.Fatalf("old code still accepted: %s", res.Body.String())
	}
	if res := fetchCode(mux, "http://share.test/a/"+id+"/?k="+fresh); res.Code != 303 {
		t.Fatalf("new code rejected: %d", res.Code)
	}

	// Withdrawing it entirely puts the artifact back to private-only, with no
	// gate to announce that anything is there.
	if err := h.Store.SetShareCode(t.Context(), id, "", nil); err != nil {
		t.Fatal(err)
	}
	res = fetchCode(mux, "http://share.test/a/"+id+"/")
	if res.Code != 404 || strings.Contains(res.Body.String(), "enter the code") {
		t.Fatalf("withdrawn: %d %s", res.Code, res.Body.String())
	}
}

// Six digits is a million codes. That is only out of reach because guessing is
// metered at the artifact — per-client limiting would hand the space to anyone
// with a few addresses.
func TestShareCodeGuessingIsMeteredPerArtifact(t *testing.T) {
	mux, _, id := codeFixture(t)
	for i := range unlockMaxAttempts {
		res := postCode(mux, id, "000000")
		if !strings.Contains(res.Body.String(), "wrong code") {
			t.Fatalf("attempt %d: %s", i, res.Body.String())
		}
	}
	// Every further guess is refused, from any client, and refused without
	// being compared: even the correct code waits out the window.
	res := postCode(mux, id, "111111")
	if res.Code != 429 || !strings.Contains(res.Body.String(), "too many attempts") {
		t.Fatalf("not locked: %d %s", res.Code, res.Body.String())
	}
	if res := fetchCode(mux, "http://share.test/a/"+id+"/?k=481523"); res.Code != 429 {
		t.Fatalf("lock did not hold against the link door: %d", res.Code)
	}
}

func TestShareCodeLockClearsAndSucceedsAfterWindow(t *testing.T) {
	mux, h, id := codeFixture(t)
	now := time.Now().UTC()
	h.unlock.now = func() time.Time { return now }
	for range unlockMaxAttempts {
		postCode(mux, id, "000000")
	}
	if postCode(mux, id, "481523").Code != 429 {
		t.Fatal("window did not apply")
	}
	now = now.Add(unlockWindow + time.Minute)
	if res := postCode(mux, id, "481523"); res.Code != 303 {
		t.Fatalf("still locked after the window: %d", res.Code)
	}
	// A success clears the count, so the next wrong guess starts over rather
	// than tipping the people holding the code straight back into the lock.
	if !h.unlock.allow(id) {
		t.Fatal("a correct code left the artifact locked")
	}
}

func TestShareCodeAcceptsRetypedForms(t *testing.T) {
	mux, _, id := codeFixture(t)
	for _, given := range []string{"481523", " 481523 ", "481-523", "481 523"} {
		if res := postCode(mux, id, given); res.Code != 303 {
			t.Fatalf("rejected %q: %d", given, res.Code)
		}
	}
	for _, given := range []string{"", "48152", "4815230", "481524"} {
		if res := postCode(mux, id, given); res.Code == 303 {
			t.Fatalf("accepted %q", given)
		}
	}
}

// A code shares one artifact, never the workspace: it must not reach anything
// the owner did not hand out.
func TestShareCodeDoesNotReachOtherArtifacts(t *testing.T) {
	mux, h, id := codeFixture(t)
	ctx := t.Context()
	a, err := h.Store.Artifact(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sibling := a
	sibling.ID = "siblingaaaaa"
	sibling.CurrentVersion = 0
	if err := h.Store.CreateArtifact(ctx, sibling); err != nil {
		t.Fatal(err)
	}
	files, err := h.Store.Files(ctx, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.AddVersion(ctx, store.Version{ArtifactID: sibling.ID, Version: 1, CreatedAt: a.CreatedAt, FileCount: len(files)}, files); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.SetVisibility(ctx, sibling.ID, "private"); err != nil {
		t.Fatal(err)
	}
	cookie := unlockCookieOf(t, fetchCode(mux, "http://share.test/a/"+id+"/?k=481523"), id)
	// Same workspace, same session-less viewer, no code of its own.
	if res := fetchCode(mux, "http://share.test/a/"+sibling.ID+"/", cookie); res.Code != 404 {
		t.Fatalf("code reached a sibling artifact: %d", res.Code)
	}
}
