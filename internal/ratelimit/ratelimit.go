// Package ratelimit is a fixed-window per-key limiter for a single process.
package ratelimit

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

type entry struct {
	windowStart time.Time
	count       int
}

type Limiter struct {
	mu     sync.Mutex
	per    int
	window time.Duration
	now    func() time.Time
	items  map[string]*entry
	sweep  time.Time
}

func New(perMinute int) *Limiter {
	return NewWindow(perMinute, time.Minute)
}

// NewWindow limits each key to per requests over the supplied window.
func NewWindow(per int, duration time.Duration) *Limiter {
	return &Limiter{per: per, window: duration, now: time.Now, items: map[string]*entry{}}
}

func (l *Limiter) SetNow(f func() time.Time) { l.now = f }

func (l *Limiter) Allow(key string) (bool, int) { return l.AllowMax(key, 0) }

// MaxKeys bounds how many keys one limiter tracks. Past it, after dropping
// expired windows, a key it has never seen is refused rather than tracked:
// a flood of fresh addresses cannot grow memory without limit, and evicting
// live entries instead would hand that flood fresh budgets.
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
	// A full map is swept at most once a second, so a flood of new keys
	// cannot turn every call into a scan of the whole map.
	if now.Sub(l.sweep) > l.window || (len(l.items) >= MaxKeys && now.Sub(l.sweep) > time.Second) {
		for k, e := range l.items {
			if now.Sub(e.windowStart) > l.window {
				delete(l.items, k)
			}
		}
		l.sweep = now
	}
	e := l.items[key]
	if e == nil && len(l.items) >= MaxKeys {
		return false, int(l.window / time.Second)
	}
	if e == nil || now.Sub(e.windowStart) >= l.window {
		l.items[key] = &entry{windowStart: now, count: 1}
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
