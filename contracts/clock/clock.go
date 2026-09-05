// Package clock is the injectable clock every time-dependent rule must use
// (PLAN.md §3 P0-b, §5 "시간 의존"): runtime offline grace (7d), HITL due (24h),
// deputy handover (half of due), overdue flags, workdir retention (14d),
// dispatched timeout (5m), heartbeat expiry (3m), rate_limited not_before.
//
// Production code takes a Clock; tests use Fake and advance it (EVAL E5-02·03,
// E7-09·10·12·13·14, E13-09~13, E14-01·02).
package clock

import (
	"sync"
	"time"
)

// Clock is the only source of "now" for server and daemon logic.
type Clock interface {
	Now() time.Time
	// Since is Now().Sub(t).
	Since(t time.Time) time.Duration
	// After returns a channel that fires once the clock passes d from Now().
	// Real: time.After. Fake: fires when Advance/Set crosses the deadline.
	After(d time.Duration) <-chan time.Time
}

// Real wraps the wall clock.
type Real struct{}

func (Real) Now() time.Time                         { return time.Now() }
func (Real) Since(t time.Time) time.Duration        { return time.Since(t) }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a manually advanced clock for tests. Safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake starts at t (use a fixed UTC instant in tests).
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Since(t time.Time) time.Duration { return f.Now().Sub(t) }

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	at := f.now.Add(d)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: at, ch: ch})
	return ch
}

// Advance moves the clock forward by d and fires every waiter whose deadline
// has passed. Advancing by zero or negative is a no-op.
func (f *Fake) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	f.Set(f.Now().Add(d))
}

// Set jumps the clock to t (must not go backwards; earlier t is ignored).
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	if t.Before(f.now) {
		f.mu.Unlock()
		return
	}
	f.now = t
	var keep []waiter
	var fire []waiter
	for _, w := range f.waiters {
		if !w.at.After(t) {
			fire = append(fire, w)
		} else {
			keep = append(keep, w)
		}
	}
	f.waiters = keep
	f.mu.Unlock()
	for _, w := range fire {
		w.ch <- t
	}
}
