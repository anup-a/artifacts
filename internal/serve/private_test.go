package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/store"
)

func TestPrivateViewerAccessAndIsolation(t *testing.T) {
	_, st, id := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a, err := st.Artifact(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sessions := &auth.Sessions{Store: st, Now: func() time.Time { return now }}
	u, tenant, err := st.SignIn(ctx, "test", "owner", "owner@example.test", "Owner", now)
	if err != nil {
		t.Fatal(err)
	}
	originalID := id
	id += "p"
	a.ID, a.Slug, a.TenantID, a.CurrentVersion = id, "", tenant.ID, 0
	if err := st.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		files, err := st.Files(ctx, originalID, version)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddVersion(ctx, store.Version{ArtifactID: id, Version: version, CreatedAt: now, FileCount: len(files)}, files); err != nil {
			t.Fatal(err)
		}
	}
	otherUser, other, err := st.SignIn(ctx, "test", "outsider", "other@example.test", "Other", now)
	if err != nil {
		t.Fatal(err)
	}
	cookie := func(userID, tenant string) *http.Cookie {
		rec := httptest.NewRecorder()
		if _, err := sessions.Create(ctx, rec, userID, tenant); err != nil {
			t.Fatal(err)
		}
		return rec.Result().Cookies()[0]
	}
	owner, outsider := cookie(u.ID, a.TenantID), cookie(otherUser.ID, other.ID)
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"), Sessions: sessions, APIOrigin: "http://app.test", ShareOrigin: "http://share.test"}
	mux := http.NewServeMux()
	h.Register(mux)
	request := func(host, path string, ck *http.Cookie, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://"+host+path, nil)
		if ck != nil {
			req.AddCookie(ck)
		}
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	public := request("share.test", "/a/"+id+"/", nil, "")
	if public.Code != 200 || public.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("public: %d %v", public.Code, public.Header())
	}
	if err := st.SetVisibility(ctx, id, "private"); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "index.html", "_cover.svg", "v/1/", "v/1/s/a.css"} {
		path := "/a/" + id + "/" + suffix
		for _, ck := range []*http.Cookie{nil, owner, outsider} {
			w := request("share.test", path, ck, public.Header().Get("ETag"))
			if w.Code != 404 || strings.Contains(w.Body.String(), "<html>") {
				t.Fatalf("public leak %s: %d %s", path, w.Code, w.Body.String())
			}
		}
		path = "/app/view/" + id + "/" + suffix
		// No redirect to sign-in: a visitor without a session is told they
		// cannot read this, not invited to log in as though it would help.
		if w := request("app.test", path, nil, ""); w.Code != 404 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
		if w := request("app.test", path, outsider, ""); w.Code != 404 {
			t.Fatalf("other workspace %s: %d", path, w.Code)
		}
		w := request("app.test", path, owner, "")
		if w.Code != 200 {
			t.Fatalf("owner %s: %d %s", path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cache %s", w.Header())
		}
		policy := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, "sandbox allow-scripts") || strings.Contains(policy, "allow-same-origin") || !strings.Contains(policy, "connect-src 'none'") {
			t.Fatalf("sandbox %s", policy)
		}
		if strings.Contains(w.Body.String(), "data-artifact=") {
			t.Fatal("private viewer injected public comments runtime")
		}
		if w := request("share.test", path, owner, ""); w.Code != 404 {
			t.Fatalf("wrong host %s: %d", path, w.Code)
		}
	}
	if err := st.SetVisibility(ctx, id, "public"); err != nil {
		t.Fatal(err)
	}
	if w := request("share.test", "/a/"+id+"/", nil, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("app.test", "/a/"+id+"/", owner, ""); w.Code != 404 {
		t.Fatal("public HTML executable on app origin")
	}
	now = now.Add(auth.SessionTTL + time.Hour)
	if w := request("app.test", "/app/view/"+id+"/", owner, ""); w.Code != 404 {
		t.Fatal("expired session allowed")
	}
}

// Every refusal on the private path must be the same response, or the address
// becomes a way of asking which artifact ids exist.
func TestPrivateDenialIsIndistinguishable(t *testing.T) {
	_, st, id := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessions := &auth.Sessions{Store: st, Now: func() time.Time { return now }}
	outsiderUser, outsiderTenant, err := st.SignIn(ctx, "test", "outsider", "other@example.test", "Other", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetVisibility(ctx, id, "private"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if _, err := sessions.Create(ctx, rec, outsiderUser.ID, outsiderTenant.ID); err != nil {
		t.Fatal(err)
	}
	outsider := rec.Result().Cookies()[0]

	var denied int
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"), Sessions: sessions,
		APIOrigin: "http://app.test", ShareOrigin: "http://share.test",
		Deny: func(w http.ResponseWriter, r *http.Request) {
			denied++
			w.WriteHeader(404)
			_, _ = w.Write([]byte("You do not have access to this artifact."))
		}}
	mux := http.NewServeMux()
	h.Register(mux)

	get := func(path string, ck *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://app.test"+path, nil)
		if ck != nil {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	// Someone else's private artifact, and an id that was never published.
	for _, path := range []string{"/app/view/" + id + "/", "/app/view/doesnotexist0/"} {
		for _, ck := range []*http.Cookie{nil, outsider} {
			w := get(path, ck)
			if w.Code != 404 || !strings.Contains(w.Body.String(), "do not have access") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("%s: cache %v", path, w.Header())
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Fatalf("%s redirected to %s", path, loc)
			}
		}
	}
	if denied != 4 {
		t.Fatalf("want every refusal rendered by the app page, got %d of 4", denied)
	}
	// The share origin keeps its bare 404 and never renders that page.
	w := get("/a/"+id+"/", nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), "do not have access") {
		t.Fatalf("share origin: %d %s", w.Code, w.Body.String())
	}
}

// A deployment that serves the dashboard and the share links from one host
// cannot isolate an uploaded page from the dashboard, so it gets no private
// viewing — but its public artifacts must keep serving from that one host.
func TestSingleOriginKeepsPublicServing(t *testing.T) {
	_, st, id := fixture(t)
	sessions := &auth.Sessions{Store: st, Now: time.Now}
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"), Sessions: sessions,
		APIOrigin: "http://one.test", ShareOrigin: "http://one.test"}
	mux := http.NewServeMux()
	h.Register(mux)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "http://one.test"+path, nil))
		return w
	}
	for _, path := range []string{"/a/" + id + "/", "/a/" + id + "/_cover.svg", "/a/" + id + "/v/1/"} {
		if w := get(path); w.Code != 200 {
			t.Fatalf("single-origin public %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := get("/app/view/" + id + "/"); w.Code != 404 {
		t.Fatalf("single origin must not offer private viewing: %d", w.Code)
	}
}
