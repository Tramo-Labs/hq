package ratelimit

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestAllowCountsAndWindowEnds(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	l := New(100)
	l.SetClock(c.now)
	r := Rule{Limit: 3, Window: time.Minute}
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k", r); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}
	c.add(20 * time.Second)
	ok, retry := l.Allow("k", r)
	if ok || retry != 40*time.Second {
		t.Fatalf("4th event: ok=%v retry=%v, want refused with 40s", ok, retry)
	}
	if ok, _ := l.Allow("other", r); !ok {
		t.Fatal("another key was limited")
	}
	c.add(40 * time.Second)
	if ok, _ := l.Allow("k", r); !ok {
		t.Fatal("still limited after the window")
	}
}

func TestExceededAddReset(t *testing.T) {
	l := New(100)
	r := Rule{Limit: 2, Window: time.Hour}
	l.Add("f", r)
	if ex, _ := l.Exceeded("f", r); ex {
		t.Fatal("exceeded after one")
	}
	l.Add("f", r)
	if ex, retry := l.Exceeded("f", r); !ex || retry <= 0 {
		t.Fatalf("not exceeded after two: %v %v", ex, retry)
	}
	l.Reset("f")
	if ex, _ := l.Exceeded("f", r); ex {
		t.Fatal("still exceeded after reset")
	}
}

// Full of live keys, the limiter keeps limiting new keys instead of
// refusing everyone or growing without bound.
func TestBoundedMemory(t *testing.T) {
	l := New(50)
	r := Rule{Limit: 1, Window: time.Hour}
	for i := 0; i < 500; i++ {
		l.Add(fmt.Sprint("ip-", i), r)
	}
	if n := len(l.buckets); n > 50 {
		t.Fatalf("%d keys tracked, cap is 50", n)
	}
	if ok, _ := l.Allow("fresh", r); !ok {
		t.Fatal("a new key was refused when the map was full")
	}
	if ok, _ := l.Allow("fresh", r); ok {
		t.Fatal("the new key was not limited")
	}
}

func TestConcurrentUse(t *testing.T) {
	l := New(1000)
	r := Rule{Limit: 100, Window: time.Minute}
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 400; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("shared", r); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 100 {
		t.Fatalf("allowed %d of 400 concurrent events, want exactly 100", allowed)
	}
}
