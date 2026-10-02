package api

import (
	"context"
	"log/slog"
	"time"
)

// RunGC deletes zero-ref blobs older than 24h on a timer until ctx is done.
func RunGC(ctx context.Context, d Deps, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := gcOnce(ctx, d, d.Now().Add(-24*time.Hour)); err != nil {
				slog.Error("gc", "err", err)
			}
		}
	}
}

// gcOnce deletes zero-ref blobs, removing DB row before physical bytes to ensure a failure leaks rather than corrupts.
// It also drops expired credentials (old revoked keys, sessions, magic links, oauth states).
func gcOnce(ctx context.Context, d Deps, olderThan time.Time) error {
	if err := d.Store.ExpireGuests(ctx, d.Now()); err != nil {
		return err
	}
	// Expired guest pages, and pages deleted before delete erased them,
	// are erased here; their blobs are then picked up below.
	unerased, err := d.Store.Unerased(ctx)
	if err != nil {
		return err
	}
	for _, id := range unerased {
		if err := d.Store.Erase(ctx, id); err != nil {
			return err
		}
	}
	if err := d.Store.DeleteExpiredCredentials(ctx, d.Now()); err != nil {
		return err
	}
	shas, err := d.Store.ZeroRefBlobs(ctx, olderThan)
	if err != nil {
		return err
	}
	for _, sha := range shas {
		// Delete DB row first; if this succeeds and physical delete fails, we leak bytes (safe).
		if err := d.Store.DeleteBlob(ctx, sha); err != nil {
			return err
		}
		// Delete physical bytes; if this fails, log and continue to avoid partial cleanup.
		if err := d.Blobs.Delete(ctx, sha); err != nil {
			slog.Warn("gc: blob delete failed; bytes leaked", "sha", sha, "err", err)
		}
	}
	return nil
}
