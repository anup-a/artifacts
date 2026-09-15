package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/config"
)

func newStore(t *testing.T) Store {
	t.Helper()
	cfg := config.Config{DB: "sqlite", DataDir: t.TempDir()}
	s, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = filepath.Join
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
	list, _ := s.ListArtifacts(ctx, k.ID, 10, time.Now().Add(time.Hour))
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}
	n, _ := s.CountArtifacts(ctx, k.ID)
	if n != 1 {
		t.Fatal("count")
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
