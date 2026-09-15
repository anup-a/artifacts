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
