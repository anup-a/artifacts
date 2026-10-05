package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mountHandler(t *testing.T) (*Handler, string, string) {
	t.Helper()
	_, st, id := fixture(t)
	a, err := st.Artifact(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("rt"), ShareOrigin: "http://share.test",
		OnView: func(_ *http.Request, id, path string) { viewsSeen = append(viewsSeen, id+" "+path) }}
	h.Register(http.NewServeMux())
	return h, id, a.TenantID
}

func serveMount(h *Handler, m Mount, method, rest string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "http://"+m.Host+m.Base+rest, nil)
	r.Header.Set("Origin", m.Scheme+"://"+m.Host)
	h.ServeMount(w, r, m, rest)
	return w
}

func TestServeMount(t *testing.T) {
	h, id, tenant := mountHandler(t)
	m := Mount{Scheme: "https", Host: "docs.example.com", Base: "/guide/", ArtifactID: id, TenantID: tenant}
	viewsSeen = nil
	w := serveMount(h, m, "GET", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "two") {
		t.Fatalf("current: %d %s", w.Code, w.Body)
	}
	if len(viewsSeen) != 1 || viewsSeen[0] != id+" index.html" {
		t.Fatalf("a view on a custom domain counts for the page: %v", viewsSeen)
	}
	if w := serveMount(h, m, "GET", "v/1/"); w.Code != 200 || !strings.Contains(w.Body.String(), "one") {
		t.Fatalf("pinned: %d %s", w.Code, w.Body)
	}
	if w := serveMount(h, m, "GET", "v/1/s/a.css"); w.Code != 200 {
		t.Fatalf("pinned asset: %d", w.Code)
	}
	if w := serveMount(h, m, "GET", "v/x/"); w.Code != 404 {
		t.Fatalf("bad version: %d", w.Code)
	}
	if w := serveMount(h, m, "DELETE", ""); w.Code != 405 {
		t.Fatalf("method: %d", w.Code)
	}
	// A page that moved to another workspace is not served on the domain.
	other := m
	other.TenantID = "t_someone_else"
	if w := serveMount(h, other, "GET", ""); w.Code != 404 {
		t.Fatalf("foreign page served: %d", w.Code)
	}
	if w := serveMount(h, other, "GET", coverPath); w.Code != 404 {
		t.Fatalf("foreign cover served: %d", w.Code)
	}
}

func TestInjectBase(t *testing.T) {
	out := string(InjectBase([]byte("<body></body>"), "abc", 2, "r1", `/a"b/`))
	if !strings.Contains(out, `data-version="2" data-base="/a&#34;b/" defer></script></body>`) {
		t.Fatalf("%s", out)
	}
}
