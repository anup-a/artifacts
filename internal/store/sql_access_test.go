package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func accessArtifact(t *testing.T, s Store, id string) {
	t.Helper()
	ctx := context.Background()
	k, err := s.CreateKey(ctx, "hash-"+id, "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateArtifact(ctx, Artifact{ID: id, KeyID: k.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestSetAccessIsOneDoorAtATime(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	accessArtifact(t, s, "acc000000001")
	a, err := s.Artifact(ctx, "acc000000001")
	if err != nil {
		t.Fatal(err)
	}
	if a.AccessMode != "" || a.AccessPassword != "" || a.AccessAllow == nil || len(a.AccessAllow) != 0 || a.AccessVersion != 0 {
		t.Fatalf("new artifact access: %+v", a)
	}
	// A private page shared by code gets a password: it stays private and
	// the code goes, in one step.
	if err := s.SetVisibility(ctx, a.ID, "private"); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := s.SetShareCode(ctx, a.ID, "123456", &future); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccess(ctx, a.ID, Access{Mode: "password", PasswordHash: "h1", Allow: []string{"ignored@x.com"}}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Artifact(ctx, a.ID)
	if a.AccessMode != "password" || a.AccessPassword != "h1" || len(a.AccessAllow) != 0 || a.ShareCode != "" || a.ShareCodeExpiresAt != nil || a.Visibility != "private" || a.AccessVersion != 1 {
		t.Fatalf("password: %+v", a)
	}
	// A public page with a door becomes private.
	if err := s.SetVisibility(ctx, a.ID, "public"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Artifact(ctx, a.ID)
	if a.Visibility != "public" || a.AccessMode != "" || a.AccessPassword != "" || a.AccessVersion != 2 {
		t.Fatalf("going public kept the door: %+v", a)
	}
	if err := s.SetAccess(ctx, a.ID, Access{Mode: "restricted", Allow: []string{"@acme.com", "sam@x.com"}, PasswordHash: "ignored"}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Artifact(ctx, a.ID)
	if a.Visibility != "private" || a.AccessMode != "restricted" || a.AccessPassword != "" || len(a.AccessAllow) != 2 || a.AccessAllow[0] != "@acme.com" || a.AccessVersion != 3 {
		t.Fatalf("restricted: %+v", a)
	}
	// Changing only the list keeps grants: they are checked against it live.
	if err := s.SetAccess(ctx, a.ID, Access{Mode: "restricted", Allow: []string{"sam@x.com"}}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Artifact(ctx, a.ID)
	if len(a.AccessAllow) != 1 || a.AccessVersion != 3 {
		t.Fatalf("list change: %+v", a)
	}
	// Mode "" clears the door and leaves visibility to the caller.
	if err := s.SetAccess(ctx, a.ID, Access{}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Artifact(ctx, a.ID)
	if a.AccessMode != "" || len(a.AccessAllow) != 0 || a.Visibility != "private" || a.AccessVersion != 4 {
		t.Fatalf("cleared: %+v", a)
	}
	if err := s.SetAccess(ctx, a.ID, Access{Mode: "bogus"}); err == nil {
		t.Fatal("bogus mode accepted")
	}
	if err := s.SetAccess(ctx, "nosuchid0000", Access{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artifact: %v", err)
	}
}

func TestAccessCodes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	accessArtifact(t, s, "acc000000002")
	now := time.Now().UTC().Truncate(time.Second)
	c := AccessCode{TokenSHA256: "tok1", ArtifactID: "acc000000002", Email: "sam@acme.com", CodeSHA256: "code1", AccessVersion: 3, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := s.CreateAccessCode(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.LiveAccessCode(ctx, c.ArtifactID, c.Email, now)
	if err != nil || got.TokenSHA256 != "tok1" || got.CodeSHA256 != "code1" || got.AccessVersion != 3 || got.ConsumedAt != nil {
		t.Fatalf("live: %v %+v", err, got)
	}
	if _, err := s.LiveAccessCode(ctx, c.ArtifactID, "other@acme.com", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other address: %v", err)
	}
	if _, err := s.LiveAccessCode(ctx, c.ArtifactID, c.Email, now.Add(16*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	// Attempts run out.
	for i := range 3 {
		if err := s.SpendAccessAttempt(ctx, "tok1", now, 3); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := s.SpendAccessAttempt(ctx, "tok1", now, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fourth attempt: %v", err)
	}
	// A new code spends the old one.
	c2 := c
	c2.TokenSHA256, c2.CodeSHA256 = "tok2", "code2"
	if err := s.CreateAccessCode(ctx, c2); err != nil {
		t.Fatal(err)
	}
	old, err := s.AccessCodeByToken(ctx, "tok1")
	if err != nil || old.ConsumedAt == nil {
		t.Fatalf("old code: %v %+v", err, old)
	}
	if got, _ := s.LiveAccessCode(ctx, c.ArtifactID, c.Email, now); got.TokenSHA256 != "tok2" {
		t.Fatalf("live after reissue: %+v", got)
	}
	// Consumed once only.
	if err := s.ConsumeAccessCode(ctx, "tok2", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeAccessCode(ctx, "tok2", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consumed twice: %v", err)
	}
	// Erasing the artifact takes its codes.
	if err := s.SoftDelete(ctx, c.ArtifactID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, c.ArtifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AccessCodeByToken(ctx, "tok2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("code survived erase: %v", err)
	}
}
