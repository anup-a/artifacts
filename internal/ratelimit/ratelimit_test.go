package ratelimit

import (
	"testing"
	"time"
)

func TestWindow(t *testing.T) {
	l := New(3)
	now := time.Unix(1000, 0)
	l.SetNow(func() time.Time { return now })
	for i := range 3 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("call %d should pass", i)
		}
	}
	ok, retry := l.Allow("k")
	if ok || retry <= 0 || retry > 60 {
		t.Fatalf("4th should be limited, retry=%d", retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("other key independent")
	}
	now = now.Add(61 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("window should reset")
	}
}

func TestHourWindow(t *testing.T) {
	clock := time.Unix(1000, 0)
	l := NewWindow(2, time.Hour)
	l.SetNow(func() time.Time { return clock })
	for range 2 {
		if ok, _ := l.Allow("email"); !ok {
			t.Fatal("early limit")
		}
	}
	clock = clock.Add(time.Minute)
	if ok, retry := l.Allow("email"); ok || retry != 3540 {
		t.Fatalf("hour limit failed: allowed=%t retry=%d", ok, retry)
	}
	clock = clock.Add(59 * time.Minute)
	if ok, _ := l.Allow("email"); !ok {
		t.Fatal("hour window did not reset")
	}
	clock = clock.Add(time.Hour + time.Second)
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("new key rejected")
	}
	if _, ok := l.items["email"]; ok {
		t.Fatal("stale bucket retained")
	}
}
