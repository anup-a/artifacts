package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignInLinksVerifiedEmails(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { s.Close() })
	now := time.Now().UTC()
	u, tenant, err := s.SignIn(ctx, "github", "42", "  Alice@Example.test  ", "Alice", now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.test" || !strings.HasPrefix(u.ID, "u_") || len(u.ID) != 14 {
		t.Fatal("user email or id is invalid")
	}
	if tenant.Kind != "personal" || tenant.Name != "alice" || tenant.Plan != "free" || !strings.HasPrefix(tenant.ID, "t_") || len(tenant.ID) != 14 {
		t.Fatal("personal tenant defaults are invalid")
	}
	linked, personal, err := s.SignIn(ctx, "email", "alice@example.test", "ALICE@example.test", "", now)
	if err != nil || linked.ID != u.ID || personal.ID != tenant.ID {
		t.Fatalf("email identity was not linked: %v", err)
	}
	existing, _, err := s.SignIn(ctx, "github", "42", "new-address@example.test", "Changed", now)
	if err != nil || existing.ID != u.ID || existing.Email != u.Email {
		t.Fatalf("existing identity changed users: %v", err)
	}
	identities, err := s.Identities(ctx, u.ID)
	if err != nil || len(identities) != 2 {
		t.Fatalf("identities: count=%d err=%v", len(identities), err)
	}
	other, otherTenant, err := s.SignIn(ctx, "email", "bob@example.test", "bob@example.test", "", now)
	if err != nil || other.ID == u.ID || otherTenant.ID == tenant.ID {
		t.Fatalf("different user shared tenant: %v", err)
	}
	if got, err := s.User(ctx, u.ID); err != nil || got.Email != u.Email {
		t.Fatalf("load user: %v", err)
	}
	if _, err := s.User(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing user must return ErrNotFound")
	}
}

func TestSessionStorageAndMembership(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { s.Close() })
	now := time.Now().UTC().Truncate(time.Second) // postgres keeps microseconds, sqlite keeps what it is given
	u, tenant, err := s.SignIn(ctx, "email", "owner@example.test", "owner@example.test", "", now)
	if err != nil {
		t.Fatal(err)
	}
	other, foreignTenant, err := s.SignIn(ctx, "email", "other@example.test", "other@example.test", "", now)
	if err != nil {
		t.Fatal(err)
	}
	in := Session{ID: "session-hash", UserID: u.ID, TenantID: tenant.ID, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour), LastSeenAt: now}
	if err := s.CreateSession(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Session(ctx, in.ID)
	if err != nil || got.TenantID != tenant.ID || !got.ExpiresAt.Equal(in.ExpiresAt) {
		t.Fatalf("session load: %v", err)
	}
	if err := s.SwitchTenant(ctx, in.ID, u.ID, foreignTenant.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tenant switch accepted: %v", err)
	}
	if err := s.SwitchTenant(ctx, in.ID, other.ID, foreignTenant.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign session switch accepted: %v", err)
	}
	workspace := Tenant{ID: "t_workspace001", Kind: "integrator", Name: "Workspace", Plan: "free", CreatedAt: now}
	if err := s.CreateTenant(ctx, workspace, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchTenant(ctx, in.ID, u.ID, workspace.ID); err != nil {
		t.Fatal(err)
	}
	refreshed := now.Add(time.Hour)
	if err := s.RefreshSession(ctx, in.ID, refreshed, refreshed.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Session(ctx, in.ID)
	if got.TenantID != workspace.ID || !got.LastSeenAt.Equal(refreshed) {
		t.Fatal("session switch or refresh not stored")
	}
	if err := s.DeleteSession(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, in.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted session found")
	}
	if err := s.RefreshSession(ctx, in.ID, refreshed, refreshed); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing session refresh accepted")
	}
	in.ID = "invalid-membership"
	in.TenantID = foreignTenant.ID
	if err := s.CreateSession(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session created without membership: %v", err)
	}
}

func TestCredentialConsumptionIsSingleUse(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { s.Close() })
	now := time.Now().UTC()
	link := MagicLink{TokenSHA256: "magic-hash", Email: "owner@example.test", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := s.CreateMagicLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConsumeMagicLink(ctx, link.TokenSHA256, now)
	if err != nil || got.Email != link.Email || got.ConsumedAt == nil {
		t.Fatalf("consume: %v", err)
	}
	if _, err := s.ConsumeMagicLink(ctx, link.TokenSHA256, now); !errors.Is(err, ErrNotFound) {
		t.Fatal("magic link replay accepted")
	}
	link.TokenSHA256 = "expired-magic-hash"
	if err := s.CreateMagicLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeMagicLink(ctx, link.TokenSHA256, link.ExpiresAt); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired magic link accepted")
	}
	if err := s.CreateOAuthState(ctx, "state-hash", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeOAuthState(ctx, "state-hash", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeOAuthState(ctx, "state-hash", now); !errors.Is(err, ErrNotFound) {
		t.Fatal("OAuth state replay accepted")
	}
	if err := s.CreateOAuthState(ctx, "expired-state", now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeOAuthState(ctx, "expired-state", now); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired OAuth state accepted")
	}
}
