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

func gcOnce(ctx context.Context, d Deps, olderThan time.Time) error {
	shas, err := d.Store.ZeroRefBlobs(ctx, olderThan)
	if err != nil {
		return err
	}
	for _, sha := range shas {
		if err := d.Blobs.Delete(ctx, sha); err != nil {
			return err
		}
		if err := d.Store.DeleteBlob(ctx, sha); err != nil {
			return err
		}
	}
	return nil
}
