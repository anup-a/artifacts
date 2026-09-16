// Package usage counts artifact page views in memory and flushes them to the
// store in batches, and provides the month arithmetic the usage page needs.
package usage

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

type bucket struct{ artifact, day string }

// Counter buffers view counts per (artifact, UTC day).
type Counter struct {
	store store.TenantStore
	mu    sync.Mutex
	buf   map[bucket]int64
}

func New(st store.TenantStore) *Counter {
	return &Counter{store: st, buf: map[bucket]int64{}}
}

// Hit records one page view of an artifact.
func (c *Counter) Hit(artifactID string, now time.Time) {
	c.mu.Lock()
	c.buf[bucket{artifactID, now.UTC().Format(time.DateOnly)}]++
	c.mu.Unlock()
}

// Flush writes the buffered counts. On failure the counts stay buffered so
// the next flush retries them.
func (c *Counter) Flush(ctx context.Context) error {
	c.mu.Lock()
	pending := c.buf
	c.buf = map[bucket]int64{}
	c.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	views := make([]store.ViewCount, 0, len(pending))
	for b, n := range pending {
		views = append(views, store.ViewCount{ArtifactID: b.artifact, Day: b.day, Count: n})
	}
	if err := c.store.AddViews(ctx, views); err != nil {
		c.mu.Lock()
		for b, n := range pending {
			c.buf[b] += n
		}
		c.mu.Unlock()
		return err
	}
	return nil
}

// Run flushes every interval until ctx is done, then flushes once more so a
// shutdown loses nothing.
func (c *Counter) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Flush(flushCtx); err != nil {
				slog.Error("usage: final flush", "err", err)
			}
			return
		case <-t.C:
			if err := c.Flush(ctx); err != nil {
				slog.Error("usage: flush", "err", err)
			}
		}
	}
}

// MonthStart is the first instant of now's calendar month in UTC.
func MonthStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// PreviousMonth is the first instant of the month before now's, in UTC.
func PreviousMonth(now time.Time) time.Time {
	return MonthStart(now).AddDate(0, -1, 0)
}
