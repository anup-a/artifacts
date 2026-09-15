// Package ratelimit is a fixed-window per-key limiter for a single process.
package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	windowStart time.Time
	count       int
}

type Limiter struct {
	mu    sync.Mutex
	per   int
	now   func() time.Time
	items map[string]*entry
	sweep time.Time
}

func New(perMinute int) *Limiter {
	return &Limiter{per: perMinute, now: time.Now, items: map[string]*entry{}}
}

func (l *Limiter) SetNow(f func() time.Time) { l.now = f }

func (l *Limiter) Allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > time.Minute {
		for k, e := range l.items {
			if now.Sub(e.windowStart) > time.Minute {
				delete(l.items, k)
			}
		}
		l.sweep = now
	}
	e := l.items[key]
	if e == nil || now.Sub(e.windowStart) >= time.Minute {
		l.items[key] = &entry{windowStart: now, count: 1}
		return true, 0
	}
	if e.count < l.per {
		e.count++
		return true, 0
	}
	retry := int(time.Minute-now.Sub(e.windowStart)) / int(time.Second)
	if retry < 1 {
		retry = 1
	}
	return false, retry
}
