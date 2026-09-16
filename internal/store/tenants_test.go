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
	now := time.Now().UTC()
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
