package store

import (
	"context"
	"testing"
	"time"
)

func TestTotals(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { s.Close() })
	now := time.Now().UTC()
	if _, _, err := s.SignIn(ctx, "email", "a@x.io", "a@x.io", "", now); err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateGuest(ctx, "guest-hash", "gst", now)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.CreateKey(ctx, "key-hash", "a@x.io")
	if err != nil {
		t.Fatal(err)
	}
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, a := range []Artifact{
		{ID: "live1", KeyID: k.ID},
		{ID: "live2", KeyID: g.ID, ExpiresAt: &future},
		{ID: "gone1", KeyID: k.ID},
		{ID: "expired1", KeyID: g.ID, ExpiresAt: &past},
	} {
		a.CreatedAt, a.UpdatedAt = now, now
		if err := s.CreateArtifact(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SoftDelete(ctx, "gone1", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Totals(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	want := Totals{Users: 1, Guests: 1, LivePages: 2, DeletedPages: 2}
	if got != want {
		t.Fatalf("Totals = %+v, want %+v", got, want)
	}
}
