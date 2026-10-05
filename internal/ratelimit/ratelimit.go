// Package ratelimit is a fixed-window per-key limiter for a single process.
package ratelimit

import (
	"container/list"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type entry struct {
	key         string
	windowStart time.Time
	count       int
}

// Limiter tracks at most MaxKeys keys. Entries sit in a list ordered by the
// start of their window, so expired windows are dropped from its front as
// calls arrive, and a full limiter makes room by forgetting the key whose
// window is oldest. It never refuses a key because it is full: a flood of
// fresh keys can push out other keys' counts, but it cannot lock anyone out.
type Limiter struct {
	mu      sync.Mutex
	per     int
	window  time.Duration
	now     func() time.Time
	items   map[string]*list.Element
	order   *list.List // of *entry, oldest window first
	maxKeys int
}

func New(perMinute int) *Limiter {
	return NewWindow(perMinute, time.Minute)
}

// NewWindow limits each key to per requests over the supplied window.
func NewWindow(per int, duration time.Duration) *Limiter {
	return &Limiter{per: per, window: duration, now: time.Now, items: map[string]*list.Element{}, order: list.New(), maxKeys: MaxKeys}
}

func (l *Limiter) SetNow(f func() time.Time) { l.now = f }

func (l *Limiter) Allow(key string) (bool, int) { return l.AllowMax(key, 0) }

// MaxKeys bounds how many keys one limiter tracks, so a flood of fresh
// addresses or emails cannot grow memory without limit. Past it the key
// with the oldest window is forgotten to make room for the new one.
const MaxKeys = 100_000

// AllowMax is Allow with a lower ceiling for this key: max, when positive
// and below the limiter's own, is the most calls the key gets per window.
func (l *Limiter) AllowMax(key string, max int) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	per := l.per
	if max > 0 && max < per {
		per = max
	}
	// Expired windows are all at the front: each call drops the ones it
	// finds there, so no call ever scans the whole map.
	for f := l.order.Front(); f != nil; f = l.order.Front() {
		e := f.Value.(*entry)
		if now.Sub(e.windowStart) < l.window {
			break
		}
		l.order.Remove(f)
		delete(l.items, e.key)
	}
	el := l.items[key]
	if el == nil {
		for len(l.items) >= l.maxKeys {
			f := l.order.Front()
			l.order.Remove(f)
			delete(l.items, f.Value.(*entry).key)
		}
		l.items[key] = l.order.PushBack(&entry{key: key, windowStart: now, count: 1})
		return true, 0
	}
	e := el.Value.(*entry)
	if now.Sub(e.windowStart) >= l.window {
		e.windowStart, e.count = now, 1
		l.order.MoveToBack(el)
		return true, 0
	}
	if e.count < per {
		e.count++
		return true, 0
	}
	retry := int((l.window - now.Sub(e.windowStart)) / time.Second)
	if retry < 1 {
		retry = 1
	}
	return false, retry
}

// ClientKey is the bucket a client address is limited under. An IPv6 client
// usually holds a whole /64, so one bucket covers the /64; IPv4 addresses
// (including IPv4-mapped IPv6) count one by one. Anything unparsable is used
// as it is.
func ClientKey(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, _ := a.WithZone("").Prefix(64)
	return p.String()
}
