package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// versioned makes an artifact with one version per body, each a single
// index.html, plus a shared.css every version carries unchanged.
func versioned(t *testing.T, s Store, id string, bodies ...string) {
	t.Helper()
	ctx := context.Background()
	k, err := s.CreateKey(ctx, "hash-"+id, id+"@x.io")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.CreateArtifact(ctx, Artifact{ID: id, KeyID: k.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, sha := range append([]string{"shared"}, bodies...) {
		if err := s.UpsertBlob(ctx, sha, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i, body := range bodies {
		n, err := s.NextVersion(ctx, id)
		if err != nil || n != i+1 {
			t.Fatalf("next version = %d %v, want %d", n, err, i+1)
		}
		files := []File{{Path: "index.html", SHA256: body, Size: 1, ContentType: "text/html"}, {Path: "shared.css", SHA256: "shared", Size: 1, ContentType: "text/css"}}
		if err := s.AddVersion(ctx, Version{ArtifactID: id, Version: n, CreatedAt: now, Bytes: 2, FileCount: 2}, files); err != nil {
			t.Fatal(err)
		}
	}
}

func refcount(t *testing.T, s Store, sha string) int {
	t.Helper()
	var n int
	if err := s.(*sqlStore).db.QueryRow(s.(*sqlStore).q(`SELECT refcount FROM blobs WHERE sha256=?`), sha).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRestoreVersionIsAPointerFlip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "restore00001", "b1", "b2", "b3")
	if err := s.SetVisibility(ctx, "restore00001", "private"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	if err := s.SetShareCode(ctx, "restore00001", "123456", &expires); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExpiry(ctx, "restore00001", &expires); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentsEnabled(ctx, "restore00001", false); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Artifact(ctx, "restore00001")
	at := time.Now().UTC().Add(time.Minute).Truncate(time.Second)

	if err := s.RestoreVersion(ctx, "restore00001", 1, at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Artifact(ctx, "restore00001")
	if got.CurrentVersion != 1 || !got.UpdatedAt.Equal(at) {
		t.Fatalf("after restore: current %d updated %v", got.CurrentVersion, got.UpdatedAt)
	}
	if got.Visibility != "private" || got.ShareCode != "123456" || got.ShareCodeExpiresAt == nil || got.ExpiresAt == nil || got.CommentsEnabled || got.Title != before.Title {
		t.Fatalf("restore changed page settings: %+v", got)
	}
	vs, _ := s.Versions(ctx, "restore00001")
	if len(vs) != 3 || vs[0].RestoredAt == nil || !vs[0].RestoredAt.Equal(at) || vs[2].RestoredAt != nil {
		t.Fatalf("versions after restore: %+v", vs)
	}
	// Nothing was copied: one reference per version still holding the file.
	if refcount(t, s, "b1") != 1 || refcount(t, s, "shared") != 3 {
		t.Fatalf("restore touched refcounts: b1 %d shared %d", refcount(t, s, "b1"), refcount(t, s, "shared"))
	}
	// The next publish continues after v3, not after the live v1.
	if n, _ := s.NextVersion(ctx, "restore00001"); n != 4 {
		t.Fatalf("next version after restore = %d, want 4", n)
	}

	if err := s.RestoreVersion(ctx, "restore00001", 1, at); !errors.Is(err, ErrLiveVersion) {
		t.Fatalf("restoring the live version: %v, want ErrLiveVersion", err)
	}
	if err := s.RestoreVersion(ctx, "restore00001", 9, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restoring a missing version: %v, want ErrNotFound", err)
	}
	if err := s.RestoreVersion(ctx, "nope00000000", 1, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restoring on a missing artifact: %v, want ErrNotFound", err)
	}
}

func TestDeleteVersion(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "delete000001", "d1", "d2", "d3")
	now := time.Now().UTC()
	th := Thread{ID: "th_on_v2", ArtifactID: "delete000001", Version: 2, AnchorJSON: `{"kind":"text","quote":"secret"}`, Status: "open", AnchorStatus: "attached", CreatedAt: now}
	if err := s.CreateThread(ctx, th, Comment{ID: "cm_on_v2", ThreadID: th.ID, AuthorName: "R", AuthorKind: "viewer", Body: "b", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeleteVersion(ctx, "delete000001", 3); !errors.Is(err, ErrLiveVersion) {
		t.Fatalf("deleting the live version: %v, want ErrLiveVersion", err)
	}
	if _, err := s.DeleteVersion(ctx, "delete000001", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting a missing version: %v, want ErrNotFound", err)
	}
	orphaned, err := s.DeleteVersion(ctx, "delete000001", 2)
	if err != nil || orphaned != 1 {
		t.Fatalf("delete v2: %d %v", orphaned, err)
	}
	if fs, _ := s.Files(ctx, "delete000001", 2); len(fs) != 0 {
		t.Fatalf("v2 files left: %+v", fs)
	}
	vs, _ := s.Versions(ctx, "delete000001")
	if len(vs) != 2 || vs[0].Version != 1 || vs[1].Version != 3 {
		t.Fatalf("versions after delete: %+v", vs)
	}
	// The thread stays, open, and is marked orphaned.
	got, err := s.Thread(ctx, "th_on_v2")
	if err != nil || got.Status != "open" || got.AnchorStatus != "orphaned" || got.Version != 2 {
		t.Fatalf("thread after delete: %+v %v", got, err)
	}
	// d2 was only in v2, so GC may take it; shared is still used by v1 and v3.
	if refcount(t, s, "d2") != 0 || refcount(t, s, "shared") != 2 {
		t.Fatalf("refcounts: d2 %d shared %d", refcount(t, s, "d2"), refcount(t, s, "shared"))
	}
	zero, _ := s.ZeroRefBlobs(ctx, time.Now().Add(time.Hour))
	if len(zero) != 1 || zero[0] != "d2" {
		t.Fatalf("zero-ref blobs = %v, want [d2]", zero)
	}
	if _, err := s.DeleteVersion(ctx, "delete000001", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting it twice: %v, want ErrNotFound", err)
	}
	if err := s.RestoreVersion(ctx, "delete000001", 2, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restoring a deleted version: %v, want ErrNotFound", err)
	}

	// Deleting the newest version after a restore never frees its number.
	if err := s.RestoreVersion(ctx, "delete000001", 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteVersion(ctx, "delete000001", 3); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.NextVersion(ctx, "delete000001"); n != 4 {
		t.Fatalf("next version after deleting the newest = %d, want 4", n)
	}
}

func TestPruneSparesTheRestoredVersion(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "prune0000001", "p1", "p2", "p3", "p4")
	if err := s.RestoreVersion(ctx, "prune0000001", 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.PruneVersions(ctx, "prune0000001", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != 2 {
		t.Fatalf("pruned %v, want [2]: the live v1 is spared", pruned)
	}
	if fs, _ := s.Files(ctx, "prune0000001", 1); len(fs) != 2 {
		t.Fatalf("live version lost its files: %+v", fs)
	}
	// Erase spares nothing, live or not.
	if err := s.SoftDelete(ctx, "prune0000001", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, "prune0000001"); err != nil {
		t.Fatal(err)
	}
	if vs, _ := s.Versions(ctx, "prune0000001"); len(vs) != 0 {
		t.Fatalf("versions left after erase: %+v", vs)
	}
}

// A page last published before last_version existed has it backfilled from
// current_version, and NextVersion still never goes backwards.
func TestNextVersionBackfill(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "backfill0001", "f1", "f2")
	if _, err := s.(*sqlStore).db.ExecContext(ctx, `UPDATE artifacts SET last_version=0`); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.NextVersion(ctx, "backfill0001"); n != 3 {
		t.Fatalf("next version = %d, want 3", n)
	}
	if _, err := s.NextVersion(ctx, "nope00000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artifact: %v", err)
	}
}
