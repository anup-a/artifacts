package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestArtifactVisibility(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	defer s.Close()
	k, err := s.CreateKey(ctx, "visibility-key", "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, visibility := range []string{"", "public", "private"} {
		id := "visibility-" + visibility
		a := Artifact{ID: id, KeyID: k.ID, Visibility: visibility, CreatedAt: now, UpdatedAt: now}
		if err := s.CreateArtifact(ctx, a); err != nil {
			t.Fatal(err)
		}
		want := visibility
		if want == "" {
			want = "public"
		}
		got, err := s.Artifact(ctx, id)
		if err != nil || got.Visibility != want {
			t.Fatalf("create %q: %+v %v", visibility, got, err)
		}
		for _, next := range []string{"private", "public"} {
			if err := s.SetVisibility(ctx, id, next); err != nil {
				t.Fatal(err)
			}
			got, err = s.Artifact(ctx, id)
			if err != nil || got.Visibility != next || !got.UpdatedAt.Equal(now) {
				t.Fatalf("update: %+v %v", got, err)
			}
		}
		for _, bad := range []string{"", "PRIVATE", "unlisted"} {
			if err := s.SetVisibility(ctx, id, bad); err == nil {
				t.Fatalf("accepted %q", bad)
			}
		}
	}
	if err := s.CreateArtifact(ctx, Artifact{ID: "invalid", KeyID: k.ID, Visibility: "unlisted", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("accepted invalid creation")
	}
	if _, err := s.Artifact(ctx, "invalid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid creation left row: %v", err)
	}
	if err := s.SetVisibility(ctx, "missing", "private"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	rows, err := s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("list: %v %v", rows, err)
	}
	for _, a := range rows {
		if a.Visibility != "public" {
			t.Fatalf("list visibility: %+v", a)
		}
	}
}
