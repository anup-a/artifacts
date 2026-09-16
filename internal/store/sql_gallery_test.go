package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func str(s string) *string { return &s }

func TestMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC().Truncate(time.Second)
	a := Artifact{ID: "gallery00001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Artifact(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != nil || got.CoverPath != nil || got.CoverStyle != "" {
		t.Fatalf("fresh artifact must have no labels: %+v", got)
	}
	if got.Tags == nil || len(got.Tags) != 0 {
		t.Fatalf("tags must read back as an empty slice, got %#v", got.Tags)
	}

	if err := s.SetMetadata(ctx, a.ID, str("demo"), []string{"a", "b"}, str("cover.svg"), "waves"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Artifact(ctx, a.ID)
	if got.Project == nil || *got.Project != "demo" || !reflect.DeepEqual(got.Tags, []string{"a", "b"}) ||
		got.CoverPath == nil || *got.CoverPath != "cover.svg" || got.CoverStyle != "waves" {
		t.Fatalf("round trip: %+v", got)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("SetMetadata must not touch updated_at: %v vs %v", got.UpdatedAt, now)
	}

	// Clearing: nil project and cover, empty tags, both nil and empty read back the same.
	for _, tags := range [][]string{nil, {}} {
		if err := s.SetMetadata(ctx, a.ID, nil, tags, nil, ""); err != nil {
			t.Fatal(err)
		}
		got, _ = s.Artifact(ctx, a.ID)
		if got.Project != nil || got.CoverPath != nil || got.CoverStyle != "" || got.Tags == nil || len(got.Tags) != 0 {
			t.Fatalf("clear with %#v: %+v", tags, got)
		}
	}
	if err := s.SetMetadata(ctx, "missing00000", nil, nil, nil, ""); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCreateArtifactStoresLabels(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC().Truncate(time.Second)
	a := Artifact{ID: "gallery00002", KeyID: k.ID, Project: str("p"), Tags: []string{"x"}, CoverStyle: "rings", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Artifact(ctx, a.ID)
	if got.Project == nil || *got.Project != "p" || !reflect.DeepEqual(got.Tags, []string{"x"}) || got.CoverStyle != "rings" {
		t.Fatalf("%+v", got)
	}
}

func TestProjectsAndProjectFilter(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, project *string, offset int) {
		t.Helper()
		at := now.Add(time.Duration(offset) * time.Second)
		if err := s.CreateArtifact(ctx, Artifact{ID: id, KeyID: k.ID, Project: project, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	mk("proj00000001", str("alpha"), 1)
	mk("proj00000002", str("alpha"), 2)
	mk("proj00000003", str("beta"), 3)
	mk("proj00000004", nil, 4)
	mk("proj00000005", str("gone"), 5)
	if err := s.SoftDelete(ctx, "proj00000005", now); err != nil {
		t.Fatal(err)
	}

	ps, err := s.Projects(ctx, k.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectCount{{"alpha", 2}, {"beta", 1}}
	if !reflect.DeepEqual(ps, want) {
		t.Fatalf("projects: got %+v want %+v", ps, want)
	}
	if other, _ := s.Projects(ctx, "t_other"); len(other) != 0 || other == nil {
		t.Fatalf("other tenant: %#v", other)
	}

	list, err := s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{Limit: 10, Project: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "proj00000002" || list[1].ID != "proj00000001" {
		t.Fatalf("filter: %+v", list)
	}
	if list, _ = s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{Limit: 10, Project: "nope"}); len(list) != 0 {
		t.Fatalf("unknown project must match nothing: %+v", list)
	}
	if list, _ = s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{Limit: 10}); len(list) != 4 {
		t.Fatalf("no filter: %d", len(list))
	}
}
