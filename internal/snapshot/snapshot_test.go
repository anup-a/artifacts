package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePacksDatabaseAndBlobs(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBName)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The row stays in the WAL, not yet checkpointed: the copy must see it.
	if _, err := db.Exec(`CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "blobs", "ab"), 0o755)
	os.WriteFile(filepath.Join(dir, "blobs", "ab", "abcd"), []byte("blob"), 0o644)
	os.WriteFile(filepath.Join(dir, "blobs", "ab", ".tmp-123"), []byte("partial"), 0o644)

	var buf bytes.Buffer
	if err := Write(context.Background(), dir, &buf); err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = b
	}
	if len(got) != 2 || string(got["blobs/ab/abcd"]) != "blob" {
		t.Fatalf("archive holds %v", keys(got))
	}

	restored := filepath.Join(t.TempDir(), DBName)
	os.WriteFile(restored, got[DBName], 0o644)
	rdb, err := sql.Open("sqlite", "file:"+restored)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	var v string
	if err := rdb.QueryRow(`SELECT v FROM t`).Scan(&v); err != nil || v != "kept" {
		t.Fatalf("restored db: %q %v", v, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".snapshot-*")); len(left) != 0 {
		t.Fatalf("temp copy left behind: %v", left)
	}
}

func TestWriteWithoutDatabaseFails(t *testing.T) {
	if err := Write(context.Background(), t.TempDir(), io.Discard); err == nil {
		t.Fatal("expected an error for an empty data dir")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
