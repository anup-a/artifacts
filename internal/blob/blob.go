// Package blob stores content-addressed bytes on local disk or S3.
package blob

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound = errors.New("blob not found")
	ErrChecksum = errors.New("blob checksum mismatch")
)

type Store interface {
	Put(ctx context.Context, sha string, r io.Reader, size int64) error
	Get(ctx context.Context, sha string) (io.ReadCloser, int64, error)
	Exists(ctx context.Context, sha string) (bool, error)
	Delete(ctx context.Context, sha string) error
	Ping(ctx context.Context) error
}

func Key(sha string) string { return "blobs/" + sha[:2] + "/" + sha }
