package api

import (
	"strconv"
	"testing"
	"time"
)

// TestIdempotencySweepsOnPut proves the map does not grow without bound: once it
// passes the sweep threshold, entries older than the TTL are dropped on put
// while entries still inside the TTL survive and stay readable.
func TestIdempotencySweepsOnPut(t *testing.T) {
	var i idempotency
	base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	// Fill up to just under the threshold with entries that are already stale.
	for n := 0; n < idemSweepThreshold-1; n++ {
		i.put("old-"+strconv.Itoa(n), "th_old", base)
	}
	// A live entry written after the stale ones must survive the sweep.
	live := base.Add(idemTTL + time.Minute)
	i.put("live", "th_live", live)
	if got := i.size(); got != idemSweepThreshold {
		t.Fatalf("expected no sweep below the threshold, size is %d", got)
	}

	// This put crosses the threshold and trips the sweep.
	i.put("fresh", "th_fresh", live)

	if got := i.size(); got != 2 {
		t.Fatalf("expected the stale entries to be swept, size is %d", got)
	}
	if id, ok := i.get("live", live); !ok || id != "th_live" {
		t.Fatalf("live entry lost: %q %v", id, ok)
	}
	if id, ok := i.get("fresh", live); !ok || id != "th_fresh" {
		t.Fatalf("fresh entry lost: %q %v", id, ok)
	}
	if _, ok := i.get("old-0", live); ok {
		t.Fatal("expired entry still readable")
	}
}

// TestIdempotencyGetExpires checks a single expired key is not returned even
// when the map is far below the sweep threshold.
func TestIdempotencyGetExpires(t *testing.T) {
	var i idempotency
	base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	i.put("k", "th_1", base)

	if id, ok := i.get("k", base.Add(time.Minute)); !ok || id != "th_1" {
		t.Fatalf("fresh get: %q %v", id, ok)
	}
	if _, ok := i.get("k", base.Add(idemTTL+time.Second)); ok {
		t.Fatal("expired key must not be returned")
	}
	if got := i.size(); got != 0 {
		t.Fatalf("expired key must be evicted on read, size is %d", got)
	}
}
