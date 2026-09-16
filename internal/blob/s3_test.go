package blob

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/config"
)

// fakeS3 is the smallest S3 surface the adapter touches: HEAD bucket, and
// HEAD, GET, PUT and DELETE on path-style object keys. Signatures are ignored.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	bucket  string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != f.bucket {
		w.WriteHeader(404)
		return
	}
	if key == "" {
		// The client resolves the bucket region before its first real call.
		if r.Method == http.MethodGet && r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><LocationConstraint>us-east-1</LocationConstraint>`)
			return
		}
		w.WriteHeader(200) // bucket exists
		return
	}
	switch r.Method {
	case http.MethodPut:
		body, err := readUpload(r)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		f.objects[key] = body
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(200)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(204)
	case http.MethodHead, http.MethodGet:
		data, ok := f.objects[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(404)
			if r.Method == http.MethodGet {
				io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
			}
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(200)
		if r.Method == http.MethodGet {
			w.Write(data)
		}
	default:
		w.WriteHeader(405)
	}
}

// readUpload unwraps the aws-chunked streaming encoding the client uses over
// plain HTTP: each chunk is "<hex size>;chunk-signature=...\r\n<data>\r\n" and
// a zero-size chunk ends the stream.
func readUpload(r *http.Request) ([]byte, error) {
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return io.ReadAll(r.Body)
	}
	br := bufio.NewReader(r.Body)
	var out []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeHex, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return out, nil
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(br, chunk); err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if _, err := br.ReadString('\n'); err != nil {
			return nil, err
		}
	}
}

func newFakeS3Store(t *testing.T) (Store, *fakeS3) {
	t.Helper()
	f := &fakeS3{objects: map[string][]byte{}, bucket: "blobs"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s, err := Open(config.Config{Storage: "s3", S3Endpoint: strings.TrimPrefix(srv.URL, "http://"), S3Bucket: "blobs",
		S3AccessKey: "access", S3SecretKey: "secret", S3UseSSL: false})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestS3RoundTripAgainstFakeServer(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeS3Store(t)
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	data := []byte("hello s3")
	sha := shaOf(data)
	if ok, err := s.Exists(ctx, sha); err != nil || ok {
		t.Fatalf("exists before put: %v %v", ok, err)
	}
	if _, _, err := s.Get(ctx, sha); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get before put: %v", err)
	}
	if err := s.Put(ctx, sha, bytes.NewReader([]byte("tampered!")), 9); !errors.Is(err, ErrChecksum) {
		t.Fatalf("checksum: %v", err)
	}
	if err := s.Put(ctx, sha, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(f.objects) != 1 {
		t.Fatalf("objects: %d", len(f.objects))
	}
	// A second put of the same sha is a no-op.
	if err := s.Put(ctx, sha, bytes.NewReader([]byte("ignored")), 7); err != nil {
		t.Fatalf("repeat put: %v", err)
	}
	if ok, err := s.Exists(ctx, sha); err != nil || !ok {
		t.Fatalf("exists after put: %v %v", ok, err)
	}
	rc, size, err := s.Get(ctx, sha)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if size != int64(len(data)) || !bytes.Equal(got, data) {
		t.Fatalf("get: %d %q", size, got)
	}
	if err := s.Delete(ctx, sha); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, _ := s.Exists(ctx, sha); ok {
		t.Fatal("still exists after delete")
	}
}

func TestOpenRejectsUnknownStorage(t *testing.T) {
	if _, err := Open(config.Config{Storage: "tape"}); err == nil {
		t.Fatal("unknown storage accepted")
	}
	if _, err := Open(config.Config{Storage: "s3", S3Endpoint: "not a host"}); err == nil {
		t.Fatal("bad endpoint accepted")
	}
}
