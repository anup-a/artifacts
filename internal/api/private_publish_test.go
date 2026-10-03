package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/anup-a/artifacts/internal/store"
)

func TestPublishVisibilityLifecycle(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<p>private report</p>"}}
	code, body := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, map[string]any{"visibility": "private"}, files))
	if code != 201 || body["visibility"] != "private" {
		t.Fatalf("create: %d %v", code, body)
	}
	id := body["id"].(string)
	privateURL := "http://api.test/app/view/" + id + "/"
	if body["url"] != privateURL || body["cover_url"] != privateURL+"_cover.svg" {
		t.Fatalf("private URLs: %v", body)
	}
	for _, change := range []struct {
		fields     map[string]any
		visibility string
	}{
		{nil, "private"},
		{map[string]any{"visibility": "public"}, "public"},
		{nil, "public"},
		{map[string]any{"visibility": "private"}, "private"},
	} {
		code, body = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, change.fields, files))
		if code != 200 || body["visibility"] != change.visibility {
			t.Fatalf("update: %d %v", code, body)
		}
		wantURL := privateURL
		if change.visibility == "public" {
			wantURL = "http://share.test/a/" + id + "/"
		}
		if body["url"] != wantURL {
			t.Fatalf("url: %v", body)
		}
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok))
	if code != 200 || body["visibility"] != "private" || body["url"] != privateURL {
		t.Fatalf("get: %d %v", code, body)
	}
	code, body = do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok))
	if code != 200 {
		t.Fatalf("list: %d %v", code, body)
	}
	if row := body["artifacts"].([]any)[0].(map[string]any); row["visibility"] != "private" || row["url"] != privateURL {
		t.Fatalf("list row: %v", row)
	}
	code, body = do(t, authReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/rollback/4", tok))
	if code != 200 || body["visibility"] != "private" || body["url"] != privateURL {
		t.Fatalf("rollback: %d %v", code, body)
	}
	other := mintKey(t, d, "personal")
	code, _ = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, other, map[string]any{"visibility": "public"}, files))
	if code != 404 {
		t.Fatalf("other workspace changed visibility: %d", code)
	}
	for _, bad := range []any{"", "PRIVATE", "unlisted", 42, true} {
		code, body = do(t, publishWith(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, map[string]any{"visibility": bad}, files))
		if code != 400 {
			t.Fatalf("invalid %v: %d %v", bad, code, body)
		}
	}
	code, body = do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, nil, files))
	if code != 201 || body["visibility"] != "public" {
		t.Fatalf("default: %d %v", code, body)
	}
}

// Inspect persisted visibility at the point new content becomes available and
// simulate a failed version mutation. Restriction must survive a failure;
// publication must wait for success.
type visibilityVersionStore struct {
	store.Store
	t    *testing.T
	want string
	fail bool
}

func (s visibilityVersionStore) CreateArtifact(ctx context.Context, a store.Artifact) error {
	if a.Visibility != s.want {
		s.t.Fatalf("initial visibility: %q; want %q", a.Visibility, s.want)
	}
	if err := s.Store.CreateArtifact(ctx, a); err != nil {
		return err
	}
	got, err := s.Store.Artifact(ctx, a.ID)
	if err != nil || got.Visibility != s.want {
		s.t.Fatalf("persisted initial visibility: %+v %v", got, err)
	}
	return nil
}

// The share code reaches the owner on the single-artifact read and nowhere
// else. Listing is the response that ends up in logs and screen shares.
func TestShareCodeOnlyOnSingleArtifactRead(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	code, body := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok,
		map[string]any{"visibility": "private"}, []pubFile{{"index.html", "<p>x</p>"}}))
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	id := body["id"].(string)
	if err := d.Store.SetShareCode(context.Background(), id, "481523", nil); err != nil {
		t.Fatal(err)
	}
	_, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok))
	if got["share_code"] != "481523" {
		t.Fatalf("owner cannot read the code back: %v", got)
	}
	if got["share_url"] == nil {
		t.Fatalf("no one-click link for the owner: %v", got)
	}
	_, list := do(t, authReq(t, "GET", srv.URL+"/api/artifacts", tok))
	for _, item := range list["artifacts"].([]any) {
		m := item.(map[string]any)
		if m["share_code"] != nil || m["share_url"] != nil {
			t.Fatalf("listing carries the share code: %v", m)
		}
	}
}

// Issuing, rotating and withdrawing a code through the API, which is what the
// CLI drives. Rotation must produce a different code, or it revokes nothing.
func TestShareCodeAPILifecycle(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, body := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok,
		map[string]any{"visibility": "private"}, []pubFile{{"index.html", "<p>x</p>"}}))
	id := body["id"].(string)
	share := srv.URL + "/api/artifacts/" + id + "/share-code"

	code, first := do(t, jsonReq(t, "POST", share, `{"action":"issue"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if code != 200 || first["share_code"] == nil || first["share_url"] == nil {
		t.Fatalf("issue: %d %v", code, first)
	}
	if got := first["share_code"].(string); len(got) != 6 {
		t.Fatalf("want six digits, got %q", got)
	}
	_, second := do(t, jsonReq(t, "POST", share, `{"action":"rotate"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if second["share_code"] == first["share_code"] {
		t.Fatalf("rotation reissued the same code: %v", second)
	}
	_, off := do(t, jsonReq(t, "POST", share, `{"action":"revoke"}`, map[string]string{"Authorization": "Bearer " + tok}))
	if off["share_code"] != nil || off["share_url"] != nil {
		t.Fatalf("revoke left a code: %v", off)
	}
	_, got := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok))
	if got["share_code"] != nil {
		t.Fatalf("withdrawn code still readable: %v", got)
	}
	if code, body := do(t, jsonReq(t, "POST", share, `{"action":"nonsense"}`, map[string]string{"Authorization": "Bearer " + tok})); code != 400 {
		t.Fatalf("bad action: %d %v", code, body)
	}
	// A public artifact has nothing to gate, so a code there would be a lie.
	_, pub := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, nil, []pubFile{{"index.html", "<p>y</p>"}}))
	pubShare := srv.URL + "/api/artifacts/" + pub["id"].(string) + "/share-code"
	if code, body := do(t, jsonReq(t, "POST", pubShare, `{"action":"issue"}`, map[string]string{"Authorization": "Bearer " + tok})); code != 400 {
		t.Fatalf("code on a public artifact: %d %v", code, body)
	}
}

func TestPrivatePublishCreatedWithRestrictedVisibility(t *testing.T) {
	_, d, tok := newTestServer(t, false)
	d.Store = visibilityVersionStore{Store: d.Store, t: t, want: "private"}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, publishWith(t, "POST", "/api/artifacts", tok,
		map[string]any{"visibility": "private"}, []pubFile{{"index.html", "x"}}))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
}

func (s visibilityVersionStore) AddVersion(ctx context.Context, v store.Version, files []store.File) error {
	a, err := s.Store.Artifact(ctx, v.ArtifactID)
	if err != nil || a.Visibility != s.want {
		s.t.Fatalf("visibility before AddVersion: %+v %v; want %s", a, err, s.want)
	}
	if s.fail {
		return errors.New("version write failed")
	}
	return s.Store.AddVersion(ctx, v, files)
}

func TestPublishVisibilityFailsClosed(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	code, body := do(t, publishWith(t, "POST", srv.URL+"/api/artifacts", tok, nil, []pubFile{{"index.html", "x"}}))
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	ctx := context.Background()
	id := body["id"].(string)
	for _, requested := range []string{"private", "public"} {
		a, err := d.Store.Artifact(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		deps := d
		deps.Store = visibilityVersionStore{Store: d.Store, t: t, want: "private", fail: true}
		s := &server{Deps: deps}
		_, _, e := s.publishVersion(ctx, a, manifest{Visibility: &requested, Files: []manifestFile{{Path: "index.html", SHA256: sha("x"), Size: 1}}})
		if e == nil {
			t.Fatal("expected injected failure")
		}
		got, err := d.Store.Artifact(ctx, id)
		if err != nil || got.Visibility != "private" || got.CurrentVersion != 1 {
			t.Fatalf("failed publish: %+v %v", got, err)
		}
	}
}
