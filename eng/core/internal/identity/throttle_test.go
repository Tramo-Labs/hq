package identity

import (
	"strconv"
	"testing"
	"time"
)

// D-17 as approved: 20 failures on one email from 3 or more distinct IPs
// within 15 minutes put it in magic link mode for one hour.
func TestAttackWatchWindowAndMode(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	fail := func(w *attackWatch, n, ips int, at time.Time) {
		for i := 0; i < n; i++ {
			w.fail("ana@x.test", "192.0.2."+strconv.Itoa(1+i%ips), at)
		}
	}

	w := newAttackWatch(DefaultLimits)
	fail(w, 19, 5, t0)
	if w.inMode("ana@x.test", t0) {
		t.Fatal("19 failures switched the email")
	}
	fail(w, 1, 1, t0.Add(14*time.Minute))
	if !w.inMode("ana@x.test", t0.Add(14*time.Minute)) {
		t.Fatal("20 failures from 5 IPs within 15 minutes did not switch the email")
	}
	if !w.inMode("ana@x.test", t0.Add(14*time.Minute+59*time.Minute)) {
		t.Fatal("the mode ended before an hour")
	}
	if w.inMode("ana@x.test", t0.Add(14*time.Minute+61*time.Minute)) {
		t.Fatal("the mode lasted more than an hour")
	}
	if w.inMode("other@x.test", t0.Add(14*time.Minute)) {
		t.Fatal("another email is in the mode")
	}

	// Spread over more than 15 minutes it is not one attack window.
	w = newAttackWatch(DefaultLimits)
	fail(w, 10, 5, t0)
	fail(w, 10, 5, t0.Add(16*time.Minute))
	if w.inMode("ana@x.test", t0.Add(16*time.Minute)) {
		t.Fatal("failures 16 minutes apart counted as one window")
	}

	// Two IPs are never enough, however many failures.
	w = newAttackWatch(DefaultLimits)
	fail(w, 100, 2, t0)
	if w.inMode("ana@x.test", t0) {
		t.Fatal("two IPs switched the email")
	}
	fail(w, 1, 1, t0) // still the same two addresses
	w.fail("ana@x.test", "198.51.100.3", t0)
	if !w.inMode("ana@x.test", t0) {
		t.Fatal("a third IP did not switch the email")
	}
}

// The distinct IP set is capped, so one email under a huge botnet costs a
// bounded amount of memory, and the whole watch is capped in emails.
func TestAttackWatchIsBounded(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	w := newAttackWatch(DefaultLimits)
	for i := 0; i < 1000; i++ {
		w.fail("ana@x.test", "10.0."+strconv.Itoa(i/250)+"."+strconv.Itoa(i%250), t0)
	}
	if n := len(w.emails["ana@x.test"].ips); n > maxTrackedIPs {
		t.Fatalf("%d IPs tracked for one email", n)
	}
	w.maxEmails = 10
	for i := 0; i < 50; i++ {
		w.fail("e"+strconv.Itoa(i)+"@x.test", "192.0.2.1", t0.Add(time.Duration(i)*time.Second))
	}
	if len(w.emails) > 10 {
		t.Fatalf("%d emails tracked with a cap of 10", len(w.emails))
	}

	// Flooding the watch with other emails never lifts an email's mode.
	w = newAttackWatch(DefaultLimits)
	w.maxEmails = 3
	for i := 0; i < 20; i++ {
		w.fail("ana@x.test", "192.0.2."+strconv.Itoa(1+i%3), t0)
	}
	for i := 0; i < 50; i++ {
		w.fail("flood"+strconv.Itoa(i)+"@x.test", "203.0.113.1", t0.Add(time.Second))
	}
	if !w.inMode("ana@x.test", t0.Add(time.Minute)) {
		t.Fatal("a flood of other emails lifted the mode")
	}
}
