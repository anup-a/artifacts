package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// seedLabelled publishes one artifact with a project label, updated at the
// given offset from base so tests control the activity order.
func seedLabelled(t *testing.T, s Store, keyID int64, id, project string, base time.Time, offset time.Duration) {
	t.Helper()
	at := base.Add(offset)
	a := Artifact{ID: id, KeyID: keyID, Title: id, CreatedAt: at, UpdatedAt: at}
	if project != "" {
		a.Project = str(project)
	}
	if err := s.CreateArtifact(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func summaryNames(list []CollectionSummary) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.Name)
	}
	return out
}

func latestIDs(c CollectionSummary) []string {
	out := []string{}
	for _, a := range c.Latest {
		out = append(out, a.ID)
	}
	return out
}

func TestCollectionsSummarisesLabelsByActivity(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i, id := range []string{"course000001", "course000002", "course000003", "course000004"} {
		seedLabelled(t, s, k.ID, id, "Course", base, time.Duration(i)*time.Minute)
	}
	seedLabelled(t, s, k.ID, "notes0000001", "Notes", base, 10*time.Minute)
	seedLabelled(t, s, k.ID, "loose0000001", "", base, 20*time.Minute)
	if err := s.SoftDelete(ctx, "course000004", base); err != nil {
		t.Fatal(err)
	}

	list, err := s.Collections(ctx, k.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryNames(list); !reflect.DeepEqual(got, []string{"Notes", "Course"}) {
		t.Fatalf("order by latest activity, unlabelled excluded: %v", got)
	}
	course := list[1]
	if course.Count != 3 {
		t.Fatalf("deleted artifacts do not count: %d", course.Count)
	}
	if got := latestIDs(course); !reflect.DeepEqual(got, []string{"course000003", "course000002", "course000001"}) {
		t.Fatalf("latest three, newest first: %v", got)
	}
	if !course.ActiveAt.Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("active at is the newest live update: %v", course.ActiveAt)
	}
	if course.Description != "" || course.ShareSlug != "" || len(course.Positions) != 0 {
		t.Fatalf("a label with no saved row has empty metadata: %+v", course.Collection)
	}
}

func TestSaveCollectionRoundTripAndShareSlug(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC().Truncate(time.Second)
	seedLabelled(t, s, k.ID, "course000001", "Course", now, 0)

	if _, err := s.Collection(ctx, k.TenantID, "Course"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no row yet: want ErrNotFound, got %v", err)
	}
	c := Collection{TenantID: k.TenantID, Name: "Course", Description: "Async Rust, start to finish", Positions: []string{"course000001"}, ShareSlug: "abc123share1"}
	if err := s.SaveCollection(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Collection(ctx, k.TenantID, "Course")
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != c.Description || !reflect.DeepEqual(got.Positions, c.Positions) || got.ShareSlug != c.ShareSlug || !got.UpdatedAt.Equal(now) {
		t.Fatalf("round trip: %+v", got)
	}
	bySlug, err := s.CollectionByShareSlug(ctx, "abc123share1")
	if err != nil || bySlug.Name != "Course" || bySlug.TenantID != k.TenantID {
		t.Fatalf("by share slug: %+v %v", bySlug, err)
	}

	// Saving again updates in place; clearing the slug unshares.
	c.Description, c.ShareSlug, c.Positions = "", "", nil
	if err := s.SaveCollection(ctx, c, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Collection(ctx, k.TenantID, "Course")
	if got.Description != "" || got.ShareSlug != "" || len(got.Positions) != 0 {
		t.Fatalf("cleared: %+v", got)
	}
	if _, err := s.CollectionByShareSlug(ctx, "abc123share1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unshared slug must not resolve: %v", err)
	}
	if _, err := s.CollectionByShareSlug(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty slug must never resolve: %v", err)
	}

	list, _ := s.Collections(ctx, k.TenantID)
	if len(list) != 1 || list[0].Name != "Course" {
		t.Fatalf("summary still lists the label: %+v", list)
	}
}

func TestRenameCollectionRelabelsAndMovesMetadata(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	other, _ := s.CreateKey(ctx, "h2", "e2")
	now := time.Now().UTC().Truncate(time.Second)
	seedLabelled(t, s, k.ID, "old000000001", "Hi-Travel", now, 0)
	seedLabelled(t, s, k.ID, "old000000002", "Hi-Travel", now, time.Minute)
	seedLabelled(t, s, other.ID, "theirs000001", "Hi-Travel", now, 0)
	if err := s.SaveCollection(ctx, Collection{TenantID: k.TenantID, Name: "Hi-Travel", Description: "trips", ShareSlug: "sharedslug01"}, now); err != nil {
		t.Fatal(err)
	}

	moved, err := s.RenameCollection(ctx, k.TenantID, "Hi-Travel", "Hi Travel", now)
	if err != nil || moved != 2 {
		t.Fatalf("rename moved %d, err %v", moved, err)
	}
	got, _ := s.Artifact(ctx, "old000000001")
	if got.Project == nil || *got.Project != "Hi Travel" {
		t.Fatalf("artifact relabelled: %+v", got.Project)
	}
	theirs, _ := s.Artifact(ctx, "theirs000001")
	if *theirs.Project != "Hi-Travel" {
		t.Fatal("another tenant's label must not change")
	}
	meta, err := s.Collection(ctx, k.TenantID, "Hi Travel")
	if err != nil || meta.Description != "trips" || meta.ShareSlug != "sharedslug01" {
		t.Fatalf("a plain rename keeps description and share link: %+v %v", meta, err)
	}
	if _, err := s.Collection(ctx, k.TenantID, "Hi-Travel"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old row gone: %v", err)
	}
}

func TestRenameCollectionMergesIntoExisting(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC().Truncate(time.Second)
	seedLabelled(t, s, k.ID, "target000001", "Hi Travel", now, 0)
	seedLabelled(t, s, k.ID, "source000001", "Hi-Travel", now, time.Minute)
	save := func(c Collection) {
		t.Helper()
		c.TenantID = k.TenantID
		if err := s.SaveCollection(ctx, c, now); err != nil {
			t.Fatal(err)
		}
	}
	save(Collection{Name: "Hi Travel", Positions: []string{"target000001"}})
	save(Collection{Name: "Hi-Travel", Description: "from the source", Positions: []string{"source000001"}, ShareSlug: "sourceslug01"})

	if _, err := s.RenameCollection(ctx, k.TenantID, "Hi-Travel", "Hi Travel", now); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Collections(ctx, k.TenantID)
	if len(list) != 1 || list[0].Name != "Hi Travel" || list[0].Count != 2 {
		t.Fatalf("merged into one collection: %+v", list)
	}
	meta := list[0].Collection
	if meta.Description != "from the source" {
		t.Fatalf("an empty target description takes the source's: %q", meta.Description)
	}
	if !reflect.DeepEqual(meta.Positions, []string{"target000001", "source000001"}) {
		t.Fatalf("source order appended after the target's: %v", meta.Positions)
	}
	if meta.ShareSlug != "" {
		t.Fatalf("a merge never publishes the target through the source's link: %q", meta.ShareSlug)
	}
	if _, err := s.CollectionByShareSlug(ctx, "sourceslug01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the source's public link stops working: %v", err)
	}
}
