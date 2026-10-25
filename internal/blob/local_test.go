package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/anup-a/byagent/internal/config"
)

func stores(t *testing.T) map[string]Store {
	t.Helper()
	out := map[string]Store{}
	l, err := Open(config.Config{Storage: "local", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out["local"] = l
	if os.Getenv("TEST_S3") == "1" {
		s, err := Open(config.Config{Storage: "s3", S3Endpoint: os.Getenv("S3_ENDPOINT"), S3Bucket: os.Getenv("S3_BUCKET"),
			S3AccessKey: os.Getenv("S3_ACCESS_KEY"), S3SecretKey: os.Getenv("S3_SECRET_KEY"), S3UseSSL: os.Getenv("S3_USE_SSL") == "true"})
		if err != nil {
			t.Fatal(err)
		}
		out["s3"] = s
	}
	return out
}

func shaOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestRoundTrip(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			data := []byte("hello artifacts")
			sha := shaOf(data)
			if ok, _ := s.Exists(ctx, sha); ok {
				t.Fatal("should not exist")
			}
			if err := s.Put(ctx, sha, bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatal(err)
			}
			if err := s.Put(ctx, sha, bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatal("second put must be idempotent:", err)
			}
			rc, n, err := s.Get(ctx, sha)
			if err != nil || n != int64(len(data)) {
				t.Fatal(err, n)
			}
			got, _ := io.ReadAll(rc)
			rc.Close()
			if !bytes.Equal(got, data) {
				t.Fatal("bytes differ")
			}
			if err := s.Delete(ctx, sha); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Get(ctx, sha); err != ErrNotFound {
				t.Fatalf("want ErrNotFound got %v", err)
			}
			if err := s.Delete(ctx, sha); err != nil {
				t.Fatal("delete absent must not error")
			}
		})
	}
}

func TestChecksumMismatch(t *testing.T) {
	s := stores(t)["local"]
	err := s.Put(context.Background(), shaOf([]byte("a")), bytes.NewReader([]byte("b")), 1)
	if err != ErrChecksum {
		t.Fatalf("want ErrChecksum got %v", err)
	}
	if ok, _ := s.Exists(context.Background(), shaOf([]byte("a"))); ok {
		t.Fatal("mismatched blob must not be stored")
	}
}

func TestKeyLayout(t *testing.T) {
	if Key("abcdef") != "blobs/ab/abcdef" {
		t.Fatal(Key("abcdef"))
	}
}
