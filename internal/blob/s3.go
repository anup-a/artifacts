package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3store struct {
	c      *minio.Client
	bucket string
}

func newS3(endpoint, bucket, access, secret string, ssl bool) (*s3store, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: ssl})
	if err != nil {
		return nil, err
	}
	return &s3store{c: c, bucket: bucket}, nil
}

func (s *s3store) Put(ctx context.Context, sha string, r io.Reader, size int64) error {
	if ok, err := s.Exists(ctx, sha); err != nil || ok {
		return err
	}
	// The body is hashed while it is read, so only small blobs ever sit in
	// memory. Verifying before the upload means a mismatch never reaches the
	// bucket and there is nothing to clean up.
	body, err := spool(r, size)
	if err != nil {
		return err
	}
	defer body.close()
	if body.sum != sha {
		return ErrChecksum
	}
	_, err = s.c.PutObject(ctx, s.bucket, Key(sha), body.r, body.size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *s3store) Get(ctx context.Context, sha string) (io.ReadCloser, int64, error) {
	obj, err := s.c.GetObject(ctx, s.bucket, Key(sha), minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return obj, st.Size, nil
}

func (s *s3store) Exists(ctx context.Context, sha string) (bool, error) {
	_, err := s.c.StatObject(ctx, s.bucket, Key(sha), minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
		return false, nil
	}
	return false, err
}

func (s *s3store) Delete(ctx context.Context, sha string) error {
	return s.c.RemoveObject(ctx, s.bucket, Key(sha), minio.RemoveObjectOptions{})
}

func (s *s3store) Ping(ctx context.Context) error {
	_, err := s.c.BucketExists(ctx, s.bucket)
	return err
}

// spoolThreshold is the largest body kept in memory. Anything bigger goes to a
// temp file so upload memory stays flat regardless of LIMIT_VERSION_BYTES.
const spoolThreshold = 4 << 20

// spoolDir is where spill files are created. Empty means the system temp dir.
// Tests point it at a scratch directory.
var spoolDir = ""

// spooledBody is a fully read body, its sha256, and a rewound reader over it.
type spooledBody struct {
	r    io.Reader
	size int64
	sum  string
	file *os.File
}

func (b *spooledBody) close() {
	if b.file == nil {
		return
	}
	name := b.file.Name()
	b.file.Close()
	os.Remove(name)
}

// spool reads at most size bytes from r, hashing as it goes, and returns the
// body ready to be uploaded. Callers must call close on the result.
func spool(r io.Reader, size int64) (*spooledBody, error) {
	h := sha256.New()
	src := io.LimitReader(r, size)
	if size <= spoolThreshold {
		data, err := io.ReadAll(io.TeeReader(src, h))
		if err != nil {
			return nil, err
		}
		return &spooledBody{r: bytes.NewReader(data), size: int64(len(data)), sum: hex.EncodeToString(h.Sum(nil))}, nil
	}
	f, err := os.CreateTemp(spoolDir, "blob-*")
	if err != nil {
		return nil, err
	}
	body := &spooledBody{file: f}
	n, err := io.Copy(io.MultiWriter(f, h), src)
	if err != nil {
		body.close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		body.close()
		return nil, err
	}
	body.r, body.size, body.sum = f, n, hex.EncodeToString(h.Sum(nil))
	return body, nil
}
