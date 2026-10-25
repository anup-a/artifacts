package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/auth"
	"github.com/anup-a/byagent/internal/store"
)

// capFixture is two private artifacts in one workspace, each an index.html
// that shows img/x.png, in two versions whose images differ. It returns the
// mux on separate app and share origins, the owner's and an outsider's
// session cookies, and a pointer to the clock.
type capWorld struct {
	mux             *http.ServeMux
	st              store.Store
	ids             [2]string
	owner, outsider *http.Cookie
	now             *time.Time
}

func capFixture(t *testing.T) capWorld {
	t.Helper()
	_, st, base := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessions := &auth.Sessions{Store: st, Now: func() time.Time { return now }}
	u, tenant, err := st.SignIn(ctx, "test", "owner", "owner@example.test", "Owner", now)
	if err != nil {
		t.Fatal(err)
	}
	otherUser, other, err := st.SignIn(ctx, "test", "outsider", "other@example.test", "Other", now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Artifact(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string) store.File {
		sum := sha256.Sum256([]byte(body))
		sha := hex.EncodeToString(sum[:])
		if err := fixtureBlobs.Put(ctx, sha, bytes.NewReader([]byte(body)), int64(len(body))); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertBlob(ctx, sha, int64(len(body))); err != nil {
			t.Fatal(err)
		}
		return store.File{SHA256: sha, Size: int64(len(body))}
	}
	var w capWorld
	for i, id := range []string{base + "c", base + "d"} {
		a.ID, a.Slug, a.TenantID, a.CurrentVersion = id, "", tenant.ID, 0
		if err := st.CreateArtifact(ctx, a); err != nil {
			t.Fatal(err)
		}
		for _, v := range []int{1, 2} {
			page := put(`<!doctype html><img src="img/x.png">`)
			page.Path, page.ContentType = "index.html", "text/html; charset=utf-8"
			img := put("PNG of version " + string(rune('0'+v)))
			img.Path, img.ContentType = "img/x.png", "image/png"
			if err := st.AddVersion(ctx, store.Version{ArtifactID: id, Version: v, CreatedAt: now, FileCount: 2}, []store.File{page, img}); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.SetVisibility(ctx, id, "private"); err != nil {
			t.Fatal(err)
		}
		w.ids[i] = id
	}
	cookie := func(userID, tenantID string) *http.Cookie {
		rec := httptest.NewRecorder()
		if _, err := sessions.Create(ctx, rec, userID, tenantID); err != nil {
			t.Fatal(err)
		}
		return rec.Result().Cookies()[0]
	}
	w.owner, w.outsider = cookie(u.ID, tenant.ID), cookie(otherUser.ID, other.ID)
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"), Sessions: sessions,
		APIOrigin: "http://app.test", ShareOrigin: "http://share.test", UnlockSecret: "test-secret",
		Now: func() time.Time { return now }}
	w.mux = http.NewServeMux()
	h.Register(w.mux)
	w.st, w.now = st, &now
	return w
}

// fetch sends GET path to the app origin with an optional session cookie and
// Sec-Fetch-Dest, the way a browser would.
func (w capWorld) fetch(path string, ck *http.Cookie, dest string) *httptest.ResponseRecorder {
	return w.fetchHost("app.test", path, ck, dest)
}

func (w capWorld) fetchHost(host, path string, ck *http.Cookie, dest string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://"+host+path, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	if dest != "" {
		req.Header.Set("Sec-Fetch-Dest", dest)
	}
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	return rec
}

var capLocation = regexp.MustCompile(`^/app/view/([a-z0-9]+)/~cap/([0-9]+\.[0-9]+\.[A-Za-z0-9_-]{22})/(.*)$`)

// open is the owner opening a private page: the redirect to its capability
// address, checked and returned.
func (w capWorld) open(t *testing.T, path string) string {
	t.Helper()
	res := w.fetch(path, w.owner, "document")
	loc := res.Header().Get("Location")
	if res.Code != http.StatusSeeOther || !capLocation.MatchString(loc) {
		t.Fatalf("open %s: %d %q", path, res.Code, loc)
	}
	if res.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("open %s: cache %v", path, res.Header())
	}
	return loc
}

// The bug: a sandboxed private page's own image request carries no session
// cookie, so it was refused and every relative asset showed broken. Under its
// capability address the same request now loads, for that artifact only.
func TestPrivatePageAssetsLoadUnderCapability(t *testing.T) {
	w := capFixture(t)
	id := w.ids[0]

	// What the page asked for before: no cookie, refused. Still refused.
	if res := w.fetch("/app/view/"+id+"/img/x.png", nil, "image"); res.Code != 404 {
		t.Fatalf("plain asset without a session: %d", res.Code)
	}

	loc := w.open(t, "/app/view/"+id+"/")
	if m := capLocation.FindStringSubmatch(loc); m[1] != id || m[3] != "" || !strings.HasPrefix(m[2], "0.") {
		t.Fatalf("location %q", loc)
	}
	// The page itself, on its top-level navigation (which carries the
	// session), renders sandboxed, exactly as before.
	doc := w.fetch(loc, w.owner, "document")
	if doc.Code != 200 || !strings.Contains(doc.Body.String(), `<img src="img/x.png">`) {
		t.Fatalf("document: %d %s", doc.Code, doc.Body.String())
	}
	policy := doc.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "sandbox allow-scripts") || strings.Contains(policy, "allow-same-origin") || !strings.Contains(policy, "connect-src 'none'") {
		t.Fatalf("sandbox %s", policy)
	}
	if doc.Header().Get("Cache-Control") != "private, no-store" || doc.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("document headers %v", doc.Header())
	}
	if strings.Contains(doc.Body.String(), "data-artifact=") {
		t.Fatal("private viewer injected the public comments runtime")
	}

	// The page's image request: no cookie, as the sandboxed document sends it.
	img := w.fetch(loc+"img/x.png", nil, "image")
	if img.Code != 200 || img.Body.String() != "PNG of version 2" {
		t.Fatalf("image under capability: %d %s", img.Code, img.Body.String())
	}
	if img.Header().Get("Cache-Control") != "private, no-store" || img.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(img.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") {
		t.Fatalf("image headers %v", img.Header())
	}
	// A page's nested frame or a script is the same.
	if res := w.fetch(loc+"index.html", nil, "iframe"); res.Code != 200 {
		t.Fatalf("frame under capability: %d", res.Code)
	}

	// A pinned version moves under a capability for that version, whose
	// assets are that version's.
	pinned := w.open(t, "/app/view/"+id+"/v/1/")
	if m := capLocation.FindStringSubmatch(pinned); !strings.HasPrefix(m[2], "1.") {
		t.Fatalf("pinned location %q", pinned)
	}
	if res := w.fetch(pinned+"img/x.png", nil, "image"); res.Code != 200 || res.Body.String() != "PNG of version 1" {
		t.Fatalf("pinned image: %d %s", res.Code, res.Body.String())
	}

	// The path and the query the owner opened are kept.
	if loc := w.open(t, "/app/view/"+id+"/index.html?tab=2"); !strings.HasSuffix(loc, "/index.html?tab=2") {
		t.Fatalf("query lost: %q", loc)
	}
	// A file that is not a page is still served in place to the session.
	if res := w.fetch("/app/view/"+id+"/img/x.png", w.owner, "document"); res.Code != 200 {
		t.Fatalf("owner opening the image: %d", res.Code)
	}
}

func TestViewCapabilityRefusals(t *testing.T) {
	w := capFixture(t)
	id, sibling := w.ids[0], w.ids[1]
	loc := w.open(t, "/app/view/"+id+"/")
	tok := capLocation.FindStringSubmatch(loc)[2]
	parts := strings.Split(tok, ".")
	flip := func(s string) string {
		if s[0] == 'A' {
			return "B" + s[1:]
		}
		return "A" + s[1:]
	}
	refused := map[string]string{
		"no capability":         "/app/view/" + id + "/~cap/img/x.png",
		"garbage":               "/app/view/" + id + "/~cap/not-a-capability/img/x.png",
		"tampered signature":    "/app/view/" + id + "/~cap/" + parts[0] + "." + parts[1] + "." + flip(parts[2]) + "/img/x.png",
		"another version":       "/app/view/" + id + "/~cap/1." + parts[1] + "." + parts[2] + "/img/x.png",
		"a later expiry":        "/app/view/" + id + "/~cap/0.9" + parts[1] + "." + parts[2] + "/img/x.png",
		"another artifact":      "/app/view/" + sibling + "/~cap/" + tok + "/img/x.png",
		"an id never published": "/app/view/doesnotexist0/~cap/" + tok + "/img/x.png",
	}
	for name, path := range refused {
		if res := w.fetch(path, nil, "image"); res.Code != 404 {
			t.Errorf("%s: %d %s", name, res.Code, res.Body.String())
		}
		// Not even with the owner's session: a bad capability is not a door.
		if res := w.fetch(path, w.owner, "image"); res.Code != 404 {
			t.Errorf("%s with session: %d", name, res.Code)
		}
	}
	// Only the app origin answers, as for /app/view itself.
	if res := w.fetchHost("share.test", loc+"img/x.png", nil, "image"); res.Code != 404 {
		t.Fatalf("share origin: %d", res.Code)
	}

	// A pasted capability address is no link to the page: a navigation needs
	// the session, and another workspace's session is refused.
	for name, ck := range map[string]*http.Cookie{"anonymous": nil, "other workspace": w.outsider} {
		if res := w.fetch(loc, ck, "document"); res.Code != 404 || strings.Contains(res.Body.String(), "<img") {
			t.Fatalf("%s navigating to the capability: %d %s", name, res.Code, res.Body.String())
		}
	}

	// Expired: assets are refused; the owner's next navigation (a reload)
	// gets a fresh capability for the same version and path.
	*w.now = w.now.Add(viewCapTTL + time.Second)
	if res := w.fetch(loc+"img/x.png", nil, "image"); res.Code != 404 {
		t.Fatalf("expired capability: %d", res.Code)
	}
	if res := w.fetch(loc, nil, "document"); res.Code != 404 {
		t.Fatalf("expired capability, no session: %d", res.Code)
	}
	if res := w.fetch(loc, w.outsider, "document"); res.Code != 404 {
		t.Fatalf("expired capability, other workspace: %d", res.Code)
	}
	fresh := w.fetch(loc, w.owner, "document")
	next := fresh.Header().Get("Location")
	if fresh.Code != http.StatusSeeOther || !capLocation.MatchString(next) || next == loc || !strings.HasPrefix(capLocation.FindStringSubmatch(next)[2], "0.") {
		t.Fatalf("reload on an expired capability: %d %q", fresh.Code, next)
	}
	if res := w.fetch(next+"img/x.png", nil, "image"); res.Code != 200 {
		t.Fatalf("fresh capability: %d", res.Code)
	}

	// The artifact moving to another workspace (a guest claim moves pages)
	// ends every capability minted for it, even one still in date.
	h := &Handler{UnlockSecret: "test-secret"}
	a, err := w.st.Artifact(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tok = capLocation.FindStringSubmatch(next)[2]
	if _, _, ok := h.readViewCap(tok, a); !ok {
		t.Fatal("capability not read back")
	}
	a.TenantID = "someone-else"
	if _, _, ok := h.readViewCap(tok, a); ok {
		t.Fatal("capability survived a move to another workspace")
	}
}

// Without a signing key a capability could be forged, so none is minted or
// honoured and the viewer serves pages where they are, as it did before.
func TestViewCapabilityNeedsAKey(t *testing.T) {
	w := capFixture(t)
	id := w.ids[0]
	loc := w.open(t, "/app/view/"+id+"/")
	h := &Handler{Store: w.st, Blobs: fixtureBlobs, Runtime: []byte("runtime"),
		Sessions:  &auth.Sessions{Store: w.st, Now: func() time.Time { return *w.now }},
		APIOrigin: "http://app.test", ShareOrigin: "http://share.test"}
	mux := http.NewServeMux()
	h.Register(mux)
	keyless := capWorld{mux: mux, owner: w.owner}
	if res := keyless.fetch("/app/view/"+id+"/", w.owner, "document"); res.Code != 200 {
		t.Fatalf("keyless page: %d", res.Code)
	}
	if res := keyless.fetch(loc+"img/x.png", nil, "image"); res.Code != 404 {
		t.Fatalf("keyless server honoured a capability: %d", res.Code)
	}
}
