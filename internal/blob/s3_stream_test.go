package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// countingReader reports how many files exist in dir at the moment the caller
// has read through half of the body. A streaming Put has already spooled the
// prefix to disk by then, a buffering one has not.
type countingReader struct {
	src     io.Reader
	dir     string
	at      int64
	read    int64
	sampled int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	c.read += int64(n)
	if c.sampled == 0 && c.read >= c.at {
		entries, rerr := os.ReadDir(c.dir)
		if rerr == nil {
			c.sampled = len(entries)
		}
	}
	return n, err
}

func randomBody(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	rnd := rand.New(rand.NewSource(1))
	if _, err := rnd.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func useSpoolDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := spoolDir
	spoolDir = dir
	t.Cleanup(func() { spoolDir = prev })
	return dir
}

func TestS3PutStreamsLargeBodyToDisk(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeS3Store(t)
	dir := useSpoolDir(t)

	data := randomBody(t, spoolThreshold+(2<<20))
	sha := shaOf(data)
	cr := &countingReader{src: bytes.NewReader(data), dir: dir, at: int64(len(data)) / 2}
	if err := s.Put(ctx, sha, cr, int64(len(data))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if cr.sampled == 0 {
		t.Fatal("body was buffered in memory, no spool file existed mid-read")
	}
	if !bytes.Equal(f.objects[Key(sha)], data) {
		t.Fatalf("stored %d bytes, want %d", len(f.objects[Key(sha)]), len(data))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spool files left behind: %d", len(entries))
	}
}

func TestS3PutLargeChecksumMismatchStoresNothing(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeS3Store(t)
	dir := useSpoolDir(t)

	data := randomBody(t, spoolThreshold+(1<<20))
	sha := shaOf(data)
	tampered := append([]byte(nil), data...)
	tampered[len(tampered)-1] ^= 0xff
	if err := s.Put(ctx, sha, bytes.NewReader(tampered), int64(len(tampered))); !errors.Is(err, ErrChecksum) {
		t.Fatalf("put tampered: %v", err)
	}
	if len(f.objects) != 0 {
		t.Fatalf("object left behind after mismatch: %d", len(f.objects))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spool files left behind: %d", len(entries))
	}
}

func TestS3PutSmallBodyStaysInMemory(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeS3Store(t)
	dir := useSpoolDir(t)

	data := []byte("small enough to keep in memory")
	sha := shaOf(data)
	if err := s.Put(ctx, sha, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if !bytes.Equal(f.objects[Key(sha)], data) {
		t.Fatalf("stored %q", f.objects[Key(sha)])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("small body spooled to disk: %d files", len(entries))
	}
}

type failingReader struct {
	src io.Reader
	err error
}

func (f *failingReader) Read(p []byte) (int, error) {
	n, err := f.src.Read(p)
	if err == io.EOF {
		return n, f.err
	}
	return n, err
}

func TestSpoolPropagatesReadErrors(t *testing.T) {
	dir := useSpoolDir(t)
	boom := errors.New("boom")

	for _, tc := range []struct {
		name string
		size int
	}{
		{"memory", 128},
		{"spilled", spoolThreshold + 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := randomBody(t, tc.size)
			r := &failingReader{src: bytes.NewReader(data), err: boom}
			if _, err := spool(r, int64(len(data))+1); !errors.Is(err, boom) {
				t.Fatalf("spool: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("spool files left behind: %d", len(entries))
			}
		})
	}
}

func TestSpoolReportsTempFileFailure(t *testing.T) {
	prev := spoolDir
	spoolDir = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { spoolDir = prev })

	data := randomBody(t, spoolThreshold+16)
	if _, err := spool(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("spool accepted an unusable temp dir")
	}
}

func TestS3PutShortBodyFailsChecksum(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeS3Store(t)
	useSpoolDir(t)

	data := randomBody(t, spoolThreshold+1024)
	sha := shaOf(data)
	// The reader ends early, so the declared size is never reached.
	if err := s.Put(ctx, sha, bytes.NewReader(data[:1024]), int64(len(data))); !errors.Is(err, ErrChecksum) {
		t.Fatalf("put truncated: %v", err)
	}
	if len(f.objects) != 0 {
		t.Fatalf("object left behind after truncated body: %d", len(f.objects))
	}
}
