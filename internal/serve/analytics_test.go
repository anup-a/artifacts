package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// Every HTML page of the current version reports a view with its path, so
// a multi-file page can be broken down; assets, pinned versions, gates and
// the owner's private viewer never do.
func TestViewHookSeesHTMLPagesOnly(t *testing.T) {
	srv, st, id := fixture(t)
	ctx := context.Background()
	files, err := st.Files(ctx, id, 2)
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, store.File{Path: "docs/b.html", SHA256: files[0].SHA256, Size: files[0].Size, ContentType: "text/html; charset=utf-8"},
		store.File{Path: "img.svg", SHA256: files[0].SHA256, Size: files[0].Size, ContentType: "image/svg+xml"})
	if err := st.AddVersion(ctx, store.Version{ArtifactID: id, Version: 3, CreatedAt: time.Now().UTC(), FileCount: len(files)}, files); err != nil {
		t.Fatal(err)
	}
	get(t, srv.URL+"/a/"+id+"/docs/b.html", nil)
	get(t, srv.URL+"/a/"+id+"/img.svg", nil)
	get(t, srv.URL+"/a/"+id+"/missing.html", nil)
	if len(viewsSeen) != 1 || viewsSeen[0] != id+" docs/b.html" {
		t.Fatalf("views %v", viewsSeen)
	}
}

func TestViewHookSkipsGateAndPrivateViewer(t *testing.T) {
	mux, h, id := codeFixture(t)
	var seen []string
	h.OnView = func(r *http.Request, id, path string) { seen = append(seen, id+" "+path) }
	if res := fetchCode(mux, "http://share.test/a/"+id+"/"); res.Code != 200 || len(seen) != 0 {
		t.Fatalf("gate counted: %d %v", res.Code, seen)
	}
	if res := postCode(mux, id, "000000"); len(seen) != 0 {
		t.Fatalf("wrong code counted: %d %v", res.Code, seen)
	}
	unlocked := unlockCookieOf(t, postCode(mux, id, "481523"), id)
	if res := fetchCode(mux, "http://share.test/a/"+id+"/", unlocked); res.Code != 200 || len(seen) != 1 {
		t.Fatalf("unlocked view: %d %v", res.Code, seen)
	}
	a, err := h.Store.Artifact(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://app.test/app/view/"+id+"/", nil)
	req = req.WithContext(context.WithValue(req.Context(), privateViewerKey{}, a.TenantID))
	// Opened with the session alone, the page moves under a capability;
	// rendered there, it is served. Neither is a view.
	w := httptest.NewRecorder()
	h.serve(w, req, id, 0, "")
	if w.Code != 303 || len(seen) != 1 {
		t.Fatalf("owner preview counted: %d %v", w.Code, seen)
	}
	req = req.WithContext(context.WithValue(req.Context(), viewCapKey{}, true))
	w = httptest.NewRecorder()
	h.serve(w, req, id, 0, "")
	if w.Code != 200 || len(seen) != 1 {
		t.Fatalf("owner preview counted: %d %v", w.Code, seen)
	}
}
