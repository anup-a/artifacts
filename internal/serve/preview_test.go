package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/auth"
	"github.com/anup-a/byagent/internal/store"
)

// The owner preview of an old version (the version list's preview_url) is the
// signed-in viewer's pinned path. It works for a public page too, and stays
// owner-only, sandboxed and uncounted however the page is shared.
func TestOwnerPreviewOfAPublicPagesOldVersion(t *testing.T) {
	_, st, src := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessions := &auth.Sessions{Store: st, Now: func() time.Time { return now }}
	u, tenant, err := st.SignIn(ctx, "test", "owner", "owner@example.test", "Owner", now)
	if err != nil {
		t.Fatal(err)
	}
	outsiderUser, outsiderTenant, err := st.SignIn(ctx, "test", "outsider", "other@example.test", "Other", now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Artifact(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	id := "preview00001"
	a.ID, a.Slug, a.TenantID, a.CurrentVersion = id, "", tenant.ID, 0
	if err := st.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2} {
		files, _ := st.Files(ctx, src, n)
		if err := st.AddVersion(ctx, store.Version{ArtifactID: id, Version: n, CreatedAt: now, FileCount: len(files)}, files); err != nil {
			t.Fatal(err)
		}
	}
	cookie := func(userID, tenantID string) *http.Cookie {
		rec := httptest.NewRecorder()
		if _, err := sessions.Create(ctx, rec, userID, tenantID); err != nil {
			t.Fatal(err)
		}
		return rec.Result().Cookies()[0]
	}
	owner, outsider := cookie(u.ID, tenant.ID), cookie(outsiderUser.ID, outsiderTenant.ID)
	var views []string
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"), Sessions: sessions, APIOrigin: "http://app.test", ShareOrigin: "http://share.test",
		OnView: func(_ *http.Request, id, _ string) { views = append(views, id) }}
	mux := http.NewServeMux()
	h.Register(mux)
	request := func(host, path string, ck *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://"+host+path, nil)
		if ck != nil {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	preview := "/app/view/" + id + "/v/1/"
	w := request("app.test", preview, owner)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "one") {
		t.Fatalf("owner preview: %d %s", w.Code, w.Body.String())
	}
	if p := w.Header().Get("Content-Security-Policy"); !strings.Contains(p, "sandbox allow-scripts") || strings.Contains(p, "allow-same-origin") {
		t.Fatalf("preview not sandboxed: %s", p)
	}
	if strings.Contains(w.Body.String(), "data-artifact=") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("preview injected the runtime or is cacheable: %v", w.Header())
	}
	if len(views) != 0 {
		t.Fatalf("a preview counted as a view: %v", views)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"anonymous":       request("app.test", preview, nil),
		"other workspace": request("app.test", preview, outsider),
		"share host":      request("share.test", preview, owner),
	} {
		if w.Code != 404 {
			t.Fatalf("%s preview: %d", name, w.Code)
		}
	}
}
