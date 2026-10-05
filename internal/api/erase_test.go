package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/guest"
	"github.com/anup-a/artifacts/internal/store"
)

// erased checks that nothing but the empty, deleted row is left of id.
func erased(t *testing.T, d Deps, id string) {
	t.Helper()
	ctx := context.Background()
	a, err := d.Store.Artifact(ctx, id)
	if err != nil || a.DeletedAt == nil || a.Title != "" || a.Project != nil || len(a.Tags) != 0 || a.ShareCode != "" {
		t.Fatalf("row not emptied: %+v %v", a, err)
	}
	if ths, err := d.Store.Threads(ctx, id, "", time.Time{}, 0); err != nil || len(ths) != 0 {
		t.Fatalf("threads left: %v %v", ths, err)
	}
	if vs, err := d.Store.Versions(ctx, id); err != nil || len(vs) != 0 {
		t.Fatalf("versions left: %v %v", vs, err)
	}
	if left, err := d.Store.Unerased(ctx); err != nil || len(left) != 0 {
		t.Fatalf("unerased: %v %v", left, err)
	}
	if rep, err := d.Store.Analytics(ctx, store.AnalyticsQuery{TenantID: a.TenantID, ArtifactID: id, ToDay: "9999", ToHour: "9999"}); err != nil || rep.AllTimeViews != 0 || len(rep.Referrers) != 0 {
		t.Fatalf("analytics left: %+v %v", rep, err)
	}
}

func TestDeleteErasesEverything(t *testing.T) {
	srv, d, tok := newTestServer(t, false)
	id := publishInto(t, srv.URL, tok, "Secret project", "<p>hello world</p>")
	if code, b := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "typo"), nil)); code != 201 {
		t.Fatalf("thread: %d %v", code, b)
	}
	if err := d.Store.AddViews(context.Background(), []store.ViewCount{{ArtifactID: id, Day: "2026-10-02", Count: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.AddViewDetails(context.Background(), []store.ViewHour{{ArtifactID: id, Hour: "2026-10-02T09", Views: 3, Visitors: 2}},
		[]store.ViewFacet{{ArtifactID: id, Day: "2026-10-02", Kind: store.FacetReferrer, Value: "news.ycombinator.com", Views: 3}}); err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, tok))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	erased(t, d, id)
	if code, _ := do(t, authReq(t, "GET", srv.URL+"/api/artifacts/"+id, tok)); code != 410 {
		t.Fatalf("get after delete: %d", code)
	}
}

func TestGCErasesExpiredGuestPages(t *testing.T) {
	srv, d, offset := newGuestServer(t)
	tok := newGuest(t, srv.URL)["token"].(string)
	id := publishOne(t, srv.URL, tok, "<p>hello world</p>")
	if code, b := do(t, jsonReq(t, "POST", srv.URL+"/c/"+id+"/comments", threadJSON(t, "Ann", "note"), nil)); code != 201 {
		t.Fatalf("thread: %d %v", code, b)
	}
	offset.Store(int64(guest.TTL + time.Minute))
	if err := gcOnce(context.Background(), d, d.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	erased(t, d, id)
}
