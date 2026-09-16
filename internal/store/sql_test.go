package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/config"
)

// newStore returns a migrated store. When TEST_PG_URL is set every test in this
// package runs against that Postgres instead of sqlite, so the dialect-specific
// SQL (placeholder rewriting, upserts, locking) is exercised for real.
func newStore(t *testing.T) Store {
	t.Helper()
	if url := os.Getenv("TEST_PG_URL"); url != "" {
		return newPGStore(t, url)
	}
	cfg := config.Config{DB: "sqlite", DataDir: t.TempDir()}
	s, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// newPGStore opens TEST_PG_URL, migrates it, and truncates every table so each
// test starts from an empty schema.
func newPGStore(t *testing.T, url string) Store {
	t.Helper()
	ctx := context.Background()
	cfg := config.Config{DB: "postgres", DatabaseURL: url}
	s, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}
	db := s.(*sqlStore).db
	if _, err := db.ExecContext(ctx, `TRUNCATE comments, threads, files, versions, artifacts, blobs, keys RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestKeysAndArtifacts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "hash1", "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.KeyByHash(ctx, "hash1"); got.ID != k.ID {
		t.Fatal("lookup")
	}
	if _, err := s.KeyByHash(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	a := Artifact{ID: "abc123abc123", KeyID: k.ID, Title: "t", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	files := []File{{Path: "index.html", SHA256: "s1", Size: 10, ContentType: "text/html"}, {Path: "a.css", SHA256: "s2", Size: 5, ContentType: "text/css"}}
	for _, f := range files {
		if err := s.UpsertBlob(ctx, f.SHA256, f.Size); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: 1, CreatedAt: now, Bytes: 15, FileCount: 2}, files); err != nil {
		t.Fatal(err)
	}
	got, err := s.Artifact(ctx, a.ID)
	if err != nil || got.CurrentVersion != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	fs, _ := s.Files(ctx, a.ID, 1)
	if len(fs) != 2 {
		t.Fatalf("files %d", len(fs))
	}
	has, _ := s.HasBlobs(ctx, []string{"s1", "zz"})
	if !has["s1"] || has["zz"] {
		t.Fatalf("%v", has)
	}
	list, _ := s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{Limit: 10, Before: time.Now().Add(time.Hour)})
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}
	n, _ := s.CountArtifacts(ctx, k.TenantID)
	if n != 1 {
		t.Fatal("count")
	}
}

func TestCommentsEnabledDefaultsOnAndToggles(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC().Truncate(time.Second)
	a := Artifact{ID: "comments0001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Artifact(ctx, a.ID)
	if err != nil || !got.CommentsEnabled {
		t.Fatalf("new artifact must have comments on: %v %+v", err, got)
	}
	if err := s.SetCommentsEnabled(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Artifact(ctx, a.ID)
	if got.CommentsEnabled {
		t.Fatal("still enabled after SetCommentsEnabled(false)")
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("toggling comments must not bump updated_at: %v", got.UpdatedAt)
	}
	list, _ := s.ListArtifacts(ctx, k.TenantID, ArtifactFilter{Limit: 10})
	if len(list) != 1 || list[0].CommentsEnabled {
		t.Fatalf("list must carry the flag: %+v", list)
	}
	if err := s.SetCommentsEnabled(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Artifact(ctx, a.ID); !got.CommentsEnabled {
		t.Fatal("not re-enabled")
	}
	if err := s.SetCommentsEnabled(ctx, "missing00001", true); err != ErrNotFound {
		t.Fatalf("unknown id: want ErrNotFound got %v", err)
	}
}

func TestPruneSkipsOpenThreads(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	a := Artifact{ID: "prune0000001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	_ = s.CreateArtifact(ctx, a)
	_ = s.UpsertBlob(ctx, "s", 1)
	for v := 1; v <= 4; v++ {
		if err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: v, CreatedAt: now.Add(time.Duration(v) * time.Second), Bytes: 1, FileCount: 1},
			[]File{{Path: "index.html", SHA256: "s", Size: 1, ContentType: "text/html"}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.CreateThread(ctx, Thread{ID: "th1", ArtifactID: a.ID, Version: 1, AnchorJSON: "{}", Status: "open", AnchorStatus: "unknown", CreatedAt: now},
		Comment{ID: "c1", ThreadID: "th1", AuthorName: "x", AuthorKind: "viewer", Body: "hi", CreatedAt: now})
	pruned, err := s.PruneVersions(ctx, a.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != 2 {
		t.Fatalf("pruned %v, want [2] (v1 kept for open thread, v3 v4 kept)", pruned)
	}
	vs, _ := s.Versions(ctx, a.ID)
	if len(vs) != 3 {
		t.Fatalf("versions %d", len(vs))
	}
	zero, _ := s.ZeroRefBlobs(ctx, time.Now().Add(time.Hour))
	if len(zero) != 0 {
		t.Fatalf("blob s still referenced, got %v", zero)
	}
}

func TestPruneDecrementsPerFileRow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	a := Artifact{ID: "prunedup00001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	_ = s.CreateArtifact(ctx, a)
	_ = s.UpsertBlob(ctx, "dup", 1)
	_ = s.UpsertBlob(ctx, "other", 1)
	// v1 has two files sharing the same sha256 ("dup").
	if err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: 1, CreatedAt: now.Add(time.Second), Bytes: 2, FileCount: 2},
		[]File{
			{Path: "index.html", SHA256: "dup", Size: 1, ContentType: "text/html"},
			{Path: "copy.html", SHA256: "dup", Size: 1, ContentType: "text/html"},
		}); err != nil {
		t.Fatal(err)
	}
	// v2 has a different sha so v1 is prunable when keep=1.
	if err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: 2, CreatedAt: now.Add(2 * time.Second), Bytes: 1, FileCount: 1},
		[]File{{Path: "other.html", SHA256: "other", Size: 1, ContentType: "text/html"}}); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.PruneVersions(ctx, a.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != 1 {
		t.Fatalf("pruned %v, want [1]", pruned)
	}
	zero, err := s.ZeroRefBlobs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(zero) != 1 || zero[0] != "dup" {
		t.Fatalf("ZeroRefBlobs = %v, want [\"dup\"]", zero)
	}
}

func TestAddVersionRejectsMissingBlob(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	a := Artifact{ID: "noblob000001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: 1, CreatedAt: now, Bytes: 1, FileCount: 1},
		[]File{{Path: "index.html", SHA256: "ghost", Size: 1, ContentType: "text/html"}})
	if !errors.Is(err, ErrBlobMissing) {
		t.Fatalf("AddVersion err = %v, want ErrBlobMissing", err)
	}
	got, err := s.Artifact(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentVersion != 0 {
		t.Fatalf("current_version = %d, want 0 (transaction must roll back)", got.CurrentVersion)
	}
	vs, _ := s.Versions(ctx, a.ID)
	if len(vs) != 0 {
		t.Fatalf("versions = %d, want 0", len(vs))
	}
}

func TestZeroRefBlobsSkipsReferencedBlobs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	a := Artifact{ID: "refcount0001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	_ = s.CreateArtifact(ctx, a)
	_ = s.UpsertBlob(ctx, "live", 1)
	if err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: 1, CreatedAt: now, Bytes: 1, FileCount: 1},
		[]File{{Path: "index.html", SHA256: "live", Size: 1, ContentType: "text/html"}}); err != nil {
		t.Fatal(err)
	}
	// Simulate refcount drift: the counter says zero but a files row still
	// points at the blob. GC must not collect it.
	db := s.(*sqlStore).db
	if _, err := db.ExecContext(ctx, s.(*sqlStore).q(`UPDATE blobs SET refcount=0 WHERE sha256=?`), "live"); err != nil {
		t.Fatal(err)
	}
	zero, err := s.ZeroRefBlobs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, sha := range zero {
		if sha == "live" {
			t.Fatal("ZeroRefBlobs returned a blob still referenced by a files row")
		}
	}
}

func TestResolveAllThreadsUnblocksPrune(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	a := Artifact{ID: "resolveall01", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	_ = s.CreateArtifact(ctx, a)
	_ = s.UpsertBlob(ctx, "s", 1)
	if err := s.AddVersion(ctx, Version{ArtifactID: a.ID, Version: 1, CreatedAt: now, Bytes: 1, FileCount: 1},
		[]File{{Path: "index.html", SHA256: "s", Size: 1, ContentType: "text/html"}}); err != nil {
		t.Fatal(err)
	}
	_ = s.CreateThread(ctx, Thread{ID: "tr1", ArtifactID: a.ID, Version: 1, AnchorJSON: "{}", Status: "open", AnchorStatus: "unknown", CreatedAt: now},
		Comment{ID: "cr1", ThreadID: "tr1", AuthorName: "x", AuthorKind: "viewer", Body: "hi", CreatedAt: now})
	if pruned, _ := s.PruneVersions(ctx, a.ID, 0); len(pruned) != 0 {
		t.Fatalf("open thread should protect v1, pruned %v", pruned)
	}
	if err := s.ResolveAllThreads(ctx, a.ID, now); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountOpenThreads(ctx, a.ID); n != 0 {
		t.Fatalf("open threads = %d, want 0", n)
	}
	pruned, err := s.PruneVersions(ctx, a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != 1 {
		t.Fatalf("pruned %v, want [1]", pruned)
	}
	zero, _ := s.ZeroRefBlobs(ctx, time.Now().Add(time.Hour))
	if len(zero) != 1 || zero[0] != "s" {
		t.Fatalf("ZeroRefBlobs = %v, want [\"s\"]", zero)
	}
}

func TestThreadsAndComments(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, _ := s.CreateKey(ctx, "h", "e")
	now := time.Now().UTC()
	a := Artifact{ID: "thread000001", KeyID: k.ID, CreatedAt: now, UpdatedAt: now}
	_ = s.CreateArtifact(ctx, a)
	th := Thread{ID: "t1", ArtifactID: a.ID, Version: 1, AnchorJSON: `{"kind":"text"}`, Status: "open", AnchorStatus: "unknown", CreatedAt: now}
	if err := s.CreateThread(ctx, th, Comment{ID: "c1", ThreadID: "t1", AuthorName: "Ann", AuthorKind: "viewer", Body: "b", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = s.AddComment(ctx, Comment{ID: "c2", ThreadID: "t1", AuthorName: "Author", AuthorKind: "owner", Body: "r", CreatedAt: now.Add(time.Second)})
	ts, _ := s.Threads(ctx, a.ID, "open", time.Time{}, 0)
	if len(ts) != 1 {
		t.Fatal("threads")
	}
	cs, _ := s.Comments(ctx, []string{"t1"})
	if len(cs["t1"]) != 2 || cs["t1"][1].AuthorKind != "owner" {
		t.Fatalf("%+v", cs)
	}
	_ = s.SetAnchorStatus(ctx, "t1", "drifted")
	_ = s.ResolveThread(ctx, "t1", now)
	got, _ := s.Thread(ctx, "t1")
	if got.Status != "resolved" || got.AnchorStatus != "drifted" || got.ResolvedAt == nil {
		t.Fatalf("%+v", got)
	}
	if n, _ := s.CountOpenThreads(ctx, a.ID); n != 0 {
		t.Fatal("open count")
	}
	byv, _ := s.CountThreadsByVersion(ctx, a.ID)
	if byv[1] != 1 {
		t.Fatalf("%v", byv)
	}
}
