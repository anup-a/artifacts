package store

import (
	"context"
	"testing"
	"time"
)

func TestTenantKeysAndEndUsers(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "first-hash", "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if k.TenantID == "" {
		t.Fatal("key must have a tenant")
	}
	tenant, err := s.Tenant(ctx, k.TenantID)
	if err != nil || tenant.Kind != "personal" || tenant.Plan != "free" {
		t.Fatalf("tenant: %v %+v", err, tenant)
	}
	now := time.Now().UTC().Truncate(time.Second) // postgres keeps microseconds, sqlite keeps what it is given
	second, err := s.CreateTenantKey(ctx, k.TenantID, "second-hash", k.Email, "Rotation", "ak_abcdef", now)
	if err != nil || second.TenantID != k.TenantID {
		t.Fatalf("rotation: %v", err)
	}
	eu, err := s.UpsertEndUser(ctx, k.TenantID, "customer/one", now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.UpsertEndUser(ctx, k.TenantID, "customer/one", now.Add(time.Hour))
	if err != nil || eu.ID != again.ID || !again.LastSeenAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("upsert: %v %+v", err, again)
	}
	a := Artifact{ID: "tenantartifact", KeyID: k.ID, TenantID: k.TenantID, EndUserID: &eu.ID, Title: "Quarterly report", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{Limit: 50, EndUser: "customer/one", Query: "report"})
	if err != nil || len(rows) != 1 || rows[0].EndUser == nil || *rows[0].EndUser != "customer/one" {
		t.Fatalf("filter: %v %+v", err, rows)
	}
	if err := s.RevokeKey(ctx, k.TenantID, k.ID, now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredCredentials(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KeyByHash(ctx, "first-hash"); err != ErrNotFound {
		t.Fatalf("revoked key retained: %v", err)
	}
	if got, err := s.Artifact(ctx, a.ID); err != nil || got.TenantID != k.TenantID {
		t.Fatalf("artifact survives key GC: %v", err)
	}
}

func TestTenantMembershipKeysAndEndUserListing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	user, _, err := s.SignIn(ctx, "email", "a@x.io", "a@x.io", "", now)
	if err != nil {
		t.Fatal(err)
	}
	acme := Tenant{ID: "t_acme", Kind: "integrator", Name: "Acme", Plan: "free", CreatedAt: now.Add(time.Second)}
	if err := s.CreateTenant(ctx, acme, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameTenant(ctx, acme.ID, "Acme Inc"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameTenant(ctx, "t_missing", "x"); err != ErrNotFound {
		t.Fatalf("rename missing: %v", err)
	}
	tenants, err := s.TenantsForUser(ctx, user.ID)
	if err != nil || len(tenants) != 2 || tenants[1].Name != "Acme Inc" || tenants[0].Kind != "personal" {
		t.Fatalf("tenants: %v %+v", err, tenants)
	}

	live, err := s.CreateTenantKey(ctx, acme.ID, "live-hash", "a@x.io", "live", "ak_live", now)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateTenantKey(ctx, acme.ID, "old-hash", "a@x.io", "old", "ak_old", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeKey(ctx, acme.ID, old.ID, now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeKey(ctx, "t_other", live.ID, now); err != ErrNotFound {
		t.Fatalf("revoke across tenants: %v", err)
	}
	if err := s.TouchKey(ctx, live.ID, now); err != nil {
		t.Fatal(err)
	}
	keys, err := s.Keys(ctx, acme.ID, now)
	if err != nil || len(keys) != 1 || keys[0].ID != live.ID || keys[0].LastUsedAt == nil || !keys[0].LastUsedAt.Equal(now) {
		t.Fatalf("keys hide month-old revocations: %v %+v", err, keys)
	}

	one, err := s.UpsertEndUser(ctx, acme.ID, "one", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertEndUser(ctx, acme.ID, "two", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	byID, err := s.EndUserByID(ctx, acme.ID, one.ID)
	if err != nil || byID.ExternalID != "one" {
		t.Fatalf("by id: %v %+v", err, byID)
	}
	if _, err := s.EndUserByID(ctx, "t_other", one.ID); err != ErrNotFound {
		t.Fatalf("end user leaks across tenants: %v", err)
	}
	all, err := s.EndUsers(ctx, acme.ID)
	if err != nil || len(all) != 2 || all[0].ExternalID != "two" {
		t.Fatalf("end users newest first: %v %+v", err, all)
	}

	a := Artifact{ID: "cursorartifact", KeyID: live.ID, TenantID: acme.ID, Title: "Draft", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTitle(ctx, a.ID, "Final"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Artifact(ctx, a.ID); got.Title != "Final" {
		t.Fatalf("title: %+v", got)
	}
	if err := s.UpsertBlob(ctx, "deadbeef", 4); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBlob(ctx, "deadbeef"); err != nil {
		t.Fatal(err)
	}
	if has, _ := s.HasBlobs(ctx, []string{"deadbeef"}); has["deadbeef"] {
		t.Fatal("blob row survived delete")
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 123456789, time.UTC)
	raw := ArtifactCursor(Artifact{ID: "abc", UpdatedAt: at})
	gotAt, gotID, err := ParseArtifactCursor(raw)
	if err != nil || gotID != "abc" || !gotAt.Equal(at) {
		t.Fatalf("round trip: %v %s %v", err, gotID, gotAt)
	}
	if gotAt, gotID, err := ParseArtifactCursor(""); err != nil || gotID != "" || !gotAt.IsZero() {
		t.Fatalf("empty cursor: %v %q %v", err, gotID, gotAt)
	}
	if gotAt, gotID, err := ParseArtifactCursor(at.Format(time.RFC3339Nano)); err != nil || gotID != "" || !gotAt.Equal(at) {
		t.Fatalf("v1 cursor: %v %q %v", err, gotID, gotAt)
	}
	for _, bad := range []string{"!!!", "bm9wZQ", "MjAyNi0wMS0wMVQwMDowMDowMFp8", "bm90LWEtdGltZXxpZA"} {
		if _, _, err := ParseArtifactCursor(bad); err == nil {
			t.Fatalf("cursor %q accepted", bad)
		}
	}
}

func TestPostgresPlaceholderRewrite(t *testing.T) {
	pg := &sqlStore{dialect: "postgres"}
	if got := pg.q(`SELECT ? FROM t WHERE a=? AND b=?`); got != `SELECT $1 FROM t WHERE a=$2 AND b=$3` {
		t.Fatalf("rewrite: %s", got)
	}
	lite := &sqlStore{dialect: "sqlite"}
	if got := lite.q(`SELECT ?`); got != `SELECT ?` {
		t.Fatalf("sqlite untouched: %s", got)
	}
}
