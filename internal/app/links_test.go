package app

import (
	"context"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/store"
)

// Links show apart from hosted pages: in their own section of the default
// list, alone under source=linked, never under source=hosted, and removing
// one only touches this workspace's list.
func TestDashboardLinks(t *testing.T) {
	e := fixture(t)
	e.signIn("owner@example.com")
	_, tenant := e.me()

	// No links: no source switch, and the first-run screen as before.
	res := e.get("/app/artifacts")
	want(t, res, 200)
	if strings.Contains(res.Body, "source-seg") {
		t.Fatal("source switch shown with no links")
	}

	now := e.clock.now()
	proj := "creatica"
	l, _, err := e.st.PutLink(context.Background(), store.Link{ID: "ln_test1", TenantID: tenant.ID, URL: "https://creatica.vercel.app/", Host: "creatica.vercel.app",
		Title: "Launch page", Project: &proj, Agent: "claude-code", CreatedAt: now, UpdatedAt: now}, 10)
	if err != nil {
		t.Fatal(err)
	}

	res = e.get("/app/artifacts")
	want(t, res, 200, "source-seg", "Linked from elsewhere", "Launch page", "Vercel", "claude-code", `href="https://creatica.vercel.app/"`)
	if strings.Contains(res.Body, "first-run") {
		t.Fatal("a workspace with links got the first-run screen")
	}
	want(t, e.get("/app/artifacts?source=linked"), 200, "Launch page", `/app/links/ln_test1/delete`)
	if res := e.get("/app/artifacts?source=hosted"); strings.Contains(res.Body, "Launch page") {
		t.Fatal("a link listed among hosted pages")
	}
	want(t, e.get("/app/artifacts?source=linked&q=nothing-matches"), 200, "No links match")

	wantRedirect(t, e.post("/app/links/"+l.ID+"/delete", "/app/artifacts?source=linked", nil), "/app/artifacts?source=linked")
	if n, _ := e.st.CountLinks(context.Background(), tenant.ID); n != 0 {
		t.Fatalf("link not removed: %d left", n)
	}
	want(t, e.post("/app/links/"+l.ID+"/delete", "/app/artifacts", nil), 404)
}
