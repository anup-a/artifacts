package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

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
	// Buffering keeps checksum verification simple; versions are capped at LIMIT_VERSION_BYTES.
	data, err := io.ReadAll(io.LimitReader(r, size))
	if err != nil {
		return err
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != sha {
		return ErrChecksum
	}
	_, err = s.c.PutObject(ctx, s.bucket, Key(sha), bytesReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
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

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
