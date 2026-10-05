package app

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/guest"
	"github.com/anup-a/artifacts/internal/store"
)

// guestWithPage makes a guest workspace holding one page, the way
// POST /api/guest and a publish would, and returns its claim path.
func guestWithPage(t *testing.T, e *env) (store.Key, string) {
	t.Helper()
	ctx := context.Background()
	now := e.clock.now()
	k, err := e.st.CreateGuest(ctx, "guest-hash", "ak_guest", now)
	if err != nil {
		t.Fatal(err)
	}
	exp := guest.ExpiresAt(now)
	a := store.Artifact{ID: "guestpage001", KeyID: k.ID, TenantID: k.TenantID, Title: "Guest draft", CreatedAt: now, UpdatedAt: now, ExpiresAt: &exp}
	if err := e.st.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	return k, "/app/claim/" + guest.ClaimCode(e.h.Cfg.KeySecret, k.TenantID)
}

func TestClaimThroughSignIn(t *testing.T) {
	e := fixture(t)
	k, claim := guestWithPage(t, e)

	// Signed out: the link survives the trip through sign-in.
	res := e.get(claim)
	want(t, res, 302)
	if res.Header.Get("Location") != "/app/login" {
		t.Fatalf("redirect %q", res.Header.Get("Location"))
	}
	e.signIn("ann@example.test")
	res = e.get("/app/")
	if res.StatusCode != 302 || res.Header.Get("Location") != claim {
		t.Fatalf("resume: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res = e.get(claim)
	want(t, res, 200, "Keep your guest pages", "Guest draft")

	res = e.post(claim, claim, url.Values{})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/app/connect?claimed=1" {
		t.Fatalf("claim: %d %s", res.StatusCode, res.Body)
	}
	want(t, e.get("/app/connect?claimed=1"), 200, "Your guest pages are in", "login --code ")
	_, tenant := e.me()
	a, err := e.st.Artifact(context.Background(), "guestpage001")
	if err != nil || a.TenantID != tenant.ID || a.ExpiresAt != nil {
		t.Fatalf("after claim: %+v %v", a, err)
	}
	// The key stays with the guest workspace, revoked: a forwarded claim link
	// must not give its sender a key into the claimer's workspace.
	key, err := e.st.KeyByHash(context.Background(), "guest-hash")
	if err != nil || key.ID != k.ID || key.TenantID == tenant.ID || key.RevokedAt == nil {
		t.Fatalf("key: %+v %v", key, err)
	}
	// The cookie was spent, and the link now does nothing.
	if res := e.get("/app/"); res.Header.Get("Location") != "/app/artifacts" {
		t.Fatalf("cookie not cleared: %q", res.Header.Get("Location"))
	}
	want(t, e.get(claim), 410, "already claimed")
}

func TestClaimRefusesExpiredAndForged(t *testing.T) {
	e := fixture(t)
	_, claim := guestWithPage(t, e)
	e.signIn("ann@example.test")
	want(t, e.get(claim+"x"), 410, "not valid")
	e.clock.t = e.clock.t.Add(guest.TTL + time.Minute)
	want(t, e.get(claim), 410, "expired")
}
