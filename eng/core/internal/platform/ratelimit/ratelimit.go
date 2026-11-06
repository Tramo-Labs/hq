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

// There is no separate check and count: checking before and counting
// after lets a burst through before anything is counted. Allow and Take
// do both in one step under the lock.

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

// Take is Allow that can be undone: it counts one event and reports
// whether it is within the quota, in one step under the lock, so a burst
// can not pass before anyone is counted. giveBack returns that unit when
// the outcome turns out not to be one the limit is for. It is safe to call
// more than once and does nothing on a refusal or once the window it came
// from has ended (a new window is never credited).
func (l *Limiter) Take(key string, r Rule) (ok bool, retry time.Duration, giveBack func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if b := l.current(key, now); b != nil && b.n >= r.Limit {
		return false, b.ends.Sub(now), func() {}
	}
	b := l.add(key, r, now)
	var once sync.Once
	return true, 0, func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if cur, live := l.buckets[key]; live && cur == b && b.n > 0 {
				b.n--
			}
		})
	}
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
