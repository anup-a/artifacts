package ratelimit

import (
	"fmt"
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

func TestAllowMaxLowersTheCeiling(t *testing.T) {
	l := NewWindow(10, time.Hour)
	for i := range 2 {
		if ok, _ := l.AllowMax("p", 2); !ok {
			t.Fatalf("call %d should pass", i)
		}
	}
	if ok, _ := l.AllowMax("p", 2); ok {
		t.Fatal("the lower ceiling must hold")
	}
	for i := range 10 {
		if ok, _ := l.AllowMax("q", 50); !ok {
			t.Fatalf("call %d should pass", i)
		}
	}
	if ok, _ := l.AllowMax("q", 50); ok {
		t.Fatal("a higher max must not raise the limiter's own ceiling")
	}
}

func TestKeyCountIsBounded(t *testing.T) {
	l := NewWindow(5, time.Hour)
	now := time.Unix(1000, 0)
	l.SetNow(func() time.Time { return now })
	for i := range MaxKeys {
		l.Allow(fmt.Sprint(i))
	}
	if ok, _ := l.Allow("fresh"); ok {
		t.Fatal("a full limiter must refuse a key it does not track")
	}
	if ok, _ := l.Allow("7"); !ok {
		t.Fatal("tracked keys keep their budget")
	}
	now = now.Add(2 * time.Hour)
	if ok, _ := l.Allow("fresh"); !ok {
		t.Fatal("expired windows free room")
	}
	if len(l.items) > MaxKeys {
		t.Fatalf("%d keys tracked", len(l.items))
	}
}

func TestClientKey(t *testing.T) {
	for in, want := range map[string]string{
		"9.9.9.9": "9.9.9.9", "2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64", "::ffff:1.2.3.4": "1.2.3.4",
		"fe80::1%eth0": "fe80::/64", "not-an-ip": "not-an-ip",
	} {
		if got := ClientKey(in); got != want {
			t.Errorf("ClientKey(%q) = %q, want %q", in, got, want)
		}
	}
}
