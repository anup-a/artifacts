package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func domainFixture(t *testing.T) (Store, string, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"t_one", "t_two"} {
		if err := s.CreateTenant(ctx, Tenant{ID: id, Kind: "personal", Name: id, CreatedAt: now}, ""); err != nil {
			t.Fatal(err)
		}
	}
	k, err := s.CreateTenantKey(ctx, "t_one", "hash-one", "a@b.c", "test", "ak_x", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Artifact{{ID: "art000000001", TenantID: "t_one"}, {ID: "art000000002", TenantID: "t_one"}} {
		a.KeyID, a.CreatedAt, a.UpdatedAt = k.ID, now, now
		if err := s.CreateArtifact(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	return s, "t_one", "t_two", now
}

func TestDomainCreateQuotaAndTaken(t *testing.T) {
	ctx := context.Background()
	s, one, two, now := domainFixture(t)
	d := Domain{Hostname: "docs.example.com", TenantID: one, Token: "tok", Provider: "none", CreatedAt: now}
	got, err := s.CreateDomain(ctx, d, 2)
	if err != nil || got.Status != "pending" {
		t.Fatalf("create: %v %+v", err, got)
	}
	// The same name, from any workspace, is taken and reports who holds it.
	existing, err := s.CreateDomain(ctx, Domain{Hostname: "docs.example.com", TenantID: two, Token: "x", CreatedAt: now}, 2)
	if !errors.Is(err, ErrDomainTaken) || existing.TenantID != one {
		t.Fatalf("taken: %v %+v", err, existing)
	}
	if _, err := s.CreateDomain(ctx, Domain{Hostname: "b.example.com", TenantID: one, Token: "t", CreatedAt: now}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDomain(ctx, Domain{Hostname: "c.example.com", TenantID: one, Token: "t", CreatedAt: now}, 2); !errors.Is(err, ErrDomainQuota) {
		t.Fatalf("quota: %v", err)
	}
	// Another workspace has its own allowance.
	if _, err := s.CreateDomain(ctx, Domain{Hostname: "c.example.com", TenantID: two, Token: "t", CreatedAt: now}, 2); err != nil {
		t.Fatal(err)
	}
	mine, err := s.Domains(ctx, one)
	if err != nil || len(mine) != 2 {
		t.Fatalf("list: %v %d", err, len(mine))
	}
	all, err := s.Domains(ctx, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("all: %v %d", err, len(all))
	}
}

func TestDomainStateAndMounts(t *testing.T) {
	ctx := context.Background()
	s, one, _, now := domainFixture(t)
	if _, err := s.CreateDomain(ctx, Domain{Hostname: "docs.example.com", TenantID: one, Token: "tok", CreatedAt: now}, 5); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Domain(ctx, "docs.example.com")
	d.Status, d.SSLStatus, d.Message, d.Provider, d.ProviderRef = "active", "active", "", "cloudflare", "ref-1"
	d.VerifiedAt, d.CheckedAt = &now, &now
	if err := s.UpdateDomainState(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Domain(ctx, "docs.example.com")
	if err != nil || got.Status != "active" || got.ProviderRef != "ref-1" || got.VerifiedAt == nil || !got.VerifiedAt.Equal(now) {
		t.Fatalf("state: %v %+v", err, got)
	}
	if err := s.UpdateDomainState(ctx, Domain{Hostname: "nope.example.com"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update: %v", err)
	}

	for _, m := range []Mount{
		{Hostname: "docs.example.com", Path: "", ArtifactID: "art000000001", CreatedAt: now},
		{Hostname: "docs.example.com", Path: "guide", ArtifactID: "art000000002", CreatedAt: now},
	} {
		if err := s.PutMount(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPrimaryMount(ctx, "art000000001", "docs.example.com", ""); err != nil {
		t.Fatal(err)
	}
	ms, _ := s.ArtifactMounts(ctx, "art000000001")
	if len(ms) != 1 || !ms[0].Primary {
		t.Fatalf("primary: %+v", ms)
	}
	// Re-pointing the same artifact keeps the mark; another artifact drops it.
	if err := s.PutMount(ctx, Mount{Hostname: "docs.example.com", Path: "", ArtifactID: "art000000001", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.ArtifactMounts(ctx, "art000000001"); len(ms) != 1 || !ms[0].Primary {
		t.Fatalf("same artifact lost primary: %+v", ms)
	}
	if err := s.PutMount(ctx, Mount{Hostname: "docs.example.com", Path: "", ArtifactID: "art000000002", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ms, _ = s.Mounts(ctx, "docs.example.com")
	if len(ms) != 2 || ms[0].ArtifactID != "art000000002" || ms[0].Primary {
		t.Fatalf("repoint: %+v", ms)
	}
	if err := s.SetPrimaryMount(ctx, "art000000001", "docs.example.com", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("primary on a mount the artifact lost: %v", err)
	}
	if err := s.DeleteMount(ctx, "docs.example.com", "guide"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMount(ctx, "docs.example.com", "guide"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if err := s.DeleteDomain(ctx, "docs.example.com"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Mounts(ctx, ""); len(ms) != 0 {
		t.Fatalf("mounts outlived their domain: %+v", ms)
	}
}

func TestEraseUnmounts(t *testing.T) {
	ctx := context.Background()
	s, one, _, now := domainFixture(t)
	if _, err := s.CreateDomain(ctx, Domain{Hostname: "docs.example.com", TenantID: one, Token: "tok", CreatedAt: now}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMount(ctx, Mount{Hostname: "docs.example.com", ArtifactID: "art000000001", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDelete(ctx, "art000000001", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, "art000000001"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Mounts(ctx, "docs.example.com"); len(ms) != 0 {
		t.Fatalf("a deleted page stays mounted: %+v", ms)
	}
}
