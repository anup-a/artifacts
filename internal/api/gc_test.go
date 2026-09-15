package api

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/blob"
)

// failingBlobStore is a blob.Store that fails on Delete.
type failingBlobStore struct {
	blob.Store
}

func (f *failingBlobStore) Delete(ctx context.Context, sha string) error {
	return io.ErrUnexpectedEOF // arbitrary error
}

func TestGCDeletesRowBeforeBytes(t *testing.T) {
	srv, d, _ := newTestServer(t, false)
	defer srv.Close()

	ctx := context.Background()

	// Seed a zero-ref blob via UpsertBlob (no version references it).
	if err := d.Store.UpsertBlob(ctx, "deadbeef", 1); err != nil {
		t.Fatal(err)
	}

	// Verify it exists before GC.
	has, err := d.Store.HasBlobs(ctx, []string{"deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	if !has["deadbeef"] {
		t.Fatal("blob should exist before gc")
	}

	// Replace Blobs with a failing decorator.
	originalBlobs := d.Blobs
	d.Blobs = &failingBlobStore{Store: originalBlobs}

	// Call gcOnce with olderThan in the future (old enough to delete).
	if err := gcOnce(ctx, d, time.Now().Add(time.Hour)); err != nil {
		t.Fatal("gcOnce should not return an error (should log and continue on blob delete failure)")
	}

	// Restore original Blobs for the assertion.
	d.Blobs = originalBlobs

	// Assert DB row was deleted (HasBlobs returns false for the blob).
	has, err = d.Store.HasBlobs(ctx, []string{"deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	if has["deadbeef"] {
		t.Fatal("blob row should be deleted from DB even though physical delete failed")
	}
}
