package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

type fakeViews struct {
	store.TenantStore
	mu    sync.Mutex
	got   []store.ViewCount
	fail  bool
	calls int
}

func (f *fakeViews) AddViews(_ context.Context, v []store.ViewCount) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return errors.New("db down")
	}
	f.got = append(f.got, v...)
	return nil
}

func TestCounterBuffersAndFlushes(t *testing.T) {
	f := &fakeViews{}
	c := New(f)
	day := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	c.Hit("a", day)
	c.Hit("a", day.Add(time.Hour))
	c.Hit("b", day)
	c.Hit("a", day.AddDate(0, 0, 1))
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"a|2026-09-16": 2, "b|2026-09-16": 1, "a|2026-09-17": 1}
	if len(f.got) != len(want) {
		t.Fatalf("flushed %+v", f.got)
	}
	for _, v := range f.got {
		if want[v.ArtifactID+"|"+v.Day] != v.Count {
			t.Fatalf("flushed %+v", f.got)
		}
	}
	// Nothing pending: no store call.
	if err := c.Flush(context.Background()); err != nil || f.calls != 1 {
		t.Fatalf("empty flush should not hit the store: calls=%d err=%v", f.calls, err)
	}
}

func TestCounterKeepsCountsWhenFlushFails(t *testing.T) {
	f := &fakeViews{fail: true}
	c := New(f)
	day := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	c.Hit("a", day)
	if err := c.Flush(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	c.Hit("a", day)
	f.fail = false
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 || f.got[0].Count != 2 {
		t.Fatalf("counts lost across a failed flush: %+v", f.got)
	}
}

func TestRunFlushesOnStop(t *testing.T) {
	f := &fakeViews{}
	c := New(f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx, time.Hour); close(done) }()
	c.Hit("a", time.Now())
	cancel()
	<-done
	if len(f.got) != 1 {
		t.Fatalf("shutdown flush missing: %+v", f.got)
	}
}

func TestMonthStart(t *testing.T) {
	now := time.Date(2026, 3, 31, 23, 0, 0, 0, time.FixedZone("x", 3600))
	if got := MonthStart(now); !got.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
	if got := PreviousMonth(now); !got.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
}
