package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func connectTenant(t *testing.T, s Store, now time.Time) Tenant {
	t.Helper()
	ten := Tenant{ID: "t_connect", Kind: "personal", Name: "Ann", CreatedAt: now}
	if err := s.CreateTenant(context.Background(), ten, ""); err != nil {
		t.Fatal(err)
	}
	return ten
}

// A dashboard code is approved when it is made: the agent redeems it once,
// by its short code, and gets a key in that workspace named as it asked.
func TestRedeemDashboardCode(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	ten := connectTenant(t, s, now)
	if err := s.CreateConnect(ctx, ConnectCode{CodeSHA256: "code1", TenantID: ten.ID, Email: "ann@example.test",
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), ApprovedAt: &now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemConnect(ctx, "code1", true, "kh0", "ak_x", "codex", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a short code is not a device secret: %v", err)
	}
	k, err := s.RedeemConnect(ctx, "code1", false, "kh1", "ak_one", "claude-code on laptop", now)
	if err != nil || k.TenantID != ten.ID || k.Name != "claude-code on laptop" || k.Email != "ann@example.test" || k.Prefix != "ak_one" {
		t.Fatalf("redeem: %+v %v", k, err)
	}
	if got, err := s.KeyByHash(ctx, "kh1"); err != nil || got.ID != k.ID {
		t.Fatalf("key not stored: %+v %v", got, err)
	}
	if _, err := s.RedeemConnect(ctx, "code1", false, "kh2", "ak_two", "again", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second redeem: %v", err)
	}
	if _, err := s.ConnectByCode(ctx, "code1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("spent code still live: %v", err)
	}
}

// A CLI-started code waits for approval, is redeemed only by its device
// secret, keeps the name the CLI asked for, and lapses after its deadline.
func TestDeviceCodeApproval(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	ten := connectTenant(t, s, now)
	c := ConnectCode{CodeSHA256: "code2", DeviceSHA256: "dev2", Name: "codex on box", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := s.CreateConnect(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemConnect(ctx, "dev2", true, "kh3", "ak_3", "", now); !errors.Is(err, ErrPending) {
		t.Fatalf("before approval: %v", err)
	}
	if _, err := s.RedeemConnect(ctx, "code2", false, "kh3", "ak_3", "", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("short code redeemed a CLI request: %v", err)
	}
	got, err := s.ConnectByCode(ctx, "code2", now)
	if err != nil || got.DeviceSHA256 != "dev2" || got.Name != "codex on box" || got.ApprovedAt != nil || got.TenantID != "" {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if err := s.ApproveConnect(ctx, "code2", ten.ID, "ann@example.test", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveConnect(ctx, "code2", "t_other", "eve@example.test", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approved twice: %v", err)
	}
	k, err := s.RedeemConnect(ctx, "dev2", true, "kh3", "ak_3", "ignored", now)
	if err != nil || k.TenantID != ten.ID || k.Name != "codex on box" {
		t.Fatalf("redeem: %+v %v", k, err)
	}

	late := ConnectCode{CodeSHA256: "code3", DeviceSHA256: "dev3", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := s.CreateConnect(ctx, late); err != nil {
		t.Fatal(err)
	}
	after := now.Add(11 * time.Minute)
	if err := s.ApproveConnect(ctx, "code3", ten.ID, "ann@example.test", after); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approved after expiry: %v", err)
	}
	if err := s.DeleteExpiredCredentials(ctx, after); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConnectByCode(ctx, "code3", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired code not collected: %v", err)
	}
}
