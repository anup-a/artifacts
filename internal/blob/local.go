package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type local struct{ root string }

func newLocal(root string) (*local, error) {
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o755); err != nil {
		return nil, err
	}
	return &local{root: root}, nil
}

func (l *local) path(sha string) string { return filepath.Join(l.root, filepath.FromSlash(Key(sha))) }

func (l *local) Put(_ context.Context, sha string, r io.Reader, size int64) error {
	dst := l.path(sha)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, size)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != sha {
		return ErrChecksum
	}
	return os.Rename(tmp.Name(), dst)
}

func (l *local) Get(_ context.Context, sha string) (io.ReadCloser, int64, error) {
	f, err := os.Open(l.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (l *local) Exists(_ context.Context, sha string) (bool, error) {
	_, err := os.Stat(l.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (l *local) Delete(_ context.Context, sha string) error {
	err := os.Remove(l.path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l *local) Ping(_ context.Context) error {
	_, err := os.Stat(filepath.Join(l.root, "blobs"))
	return err
}
