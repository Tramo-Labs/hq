// Package ratelimit counts events per key in fixed windows, in memory.
//
// It is per process: with several api replicas each one counts on its own,
// so the effective limit is multiplied by the number of replicas. Fine for
// one VPS; a shared store is the follow-up when the api scales out.
package ratelimit

import (
	"sync"
	"time"
)

// Rule allows Limit events per Window.
type Rule struct {
	Limit  int
	Window time.Duration
}

type bucket struct {
	start time.Time
	n     int
	ends  time.Time
}

// Limiter is safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	buckets map[string]*bucket
	maxKeys int
	calls   int
}

// New returns a limiter that tracks at most maxKeys keys at a time.
func New(maxKeys int) *Limiter {
	return &Limiter{now: time.Now, buckets: map[string]*bucket{}, maxKeys: maxKeys}
}

// SetClock replaces the clock (tests).
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// current returns the live bucket for key, or nil. Caller holds mu.
func (l *Limiter) current(key string, now time.Time) *bucket {
	b, ok := l.buckets[key]
	if !ok || !now.Before(b.ends) {
		return nil
	}
	return b
}

// Exceeded reports whether key already used its quota, and how long until
// the window ends. It does not count.
func (l *Limiter) Exceeded(key string, r Rule) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if b := l.current(key, now); b != nil && b.n >= r.Limit {
		return true, b.ends.Sub(now)
	}
	return false, 0
}

// Add counts one event for key.
func (l *Limiter) Add(key string, r Rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.add(key, r, l.now())
}

func (l *Limiter) add(key string, r Rule, now time.Time) *bucket {
	b := l.current(key, now)
	if b == nil {
		l.gc(now)
		b = &bucket{start: now, ends: now.Add(r.Window)}
		l.buckets[key] = b
	}
	b.n++
	return b
}

// Allow counts one event and reports whether it is within the quota.
func (l *Limiter) Allow(key string, r Rule) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if b := l.current(key, now); b != nil && b.n >= r.Limit {
		return false, b.ends.Sub(now)
	}
	l.add(key, r, now)
	return true, 0
}

// Reset forgets key (a successful login clears its failures).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// gc drops expired buckets every so often, and always when the map is
// full. When it is full of live keys, the oldest window goes first: the
// limiter keeps working instead of refusing everyone. Caller holds mu.
func (l *Limiter) gc(now time.Time) {
	l.calls++
	if l.calls%1024 != 0 && len(l.buckets) < l.maxKeys {
		return
	}
	for k, b := range l.buckets {
		if !now.Before(b.ends) {
			delete(l.buckets, k)
		}
	}
	for len(l.buckets) >= l.maxKeys {
		var oldest string
		var at time.Time
		for k, b := range l.buckets {
			if oldest == "" || b.start.Before(at) {
				oldest, at = k, b.start
			}
		}
		delete(l.buckets, oldest)
	}
}
