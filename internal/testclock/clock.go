// Package testclock provides manually advanced time for lifecycle tests. Callback
// dispatch is separate from elapsed time so tests can hold already-fired timers.
package testclock

import (
	"sort"
	"sync"
	"time"
)

type Clock struct {
	mu       sync.Mutex
	now      time.Time
	sequence uint64
	timers   map[*Timer]struct{}
}

type Timer struct {
	clock    *Clock
	deadline time.Time
	sequence uint64
	callback func()
}

func New() *Clock {
	return &Clock{now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), timers: make(map[*Timer]struct{})}
}

func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *Clock) AfterFunc(d time.Duration, callback func()) *Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	timer := &Timer{c, c.now.Add(d), c.sequence, callback}
	c.timers[timer] = struct{}{}
	return timer
}

func (t *Timer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, pending := t.clock.timers[t]
	delete(t.clock.timers, t)
	return pending
}

func (c *Clock) Pending() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.timers) }

// Elapse changes time without dispatching callbacks, modeling scheduler delay.
func (c *Clock) Elapse(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

func (c *Clock) Advance(d time.Duration) { c.Elapse(d); c.RunDue() }

func (c *Clock) RunDue() {
	for {
		callbacks := c.Take(false)
		if len(callbacks) == 0 {
			return
		}
		for _, callback := range callbacks {
			callback()
		}
	}
}

// Take marks timers fired before returning their callbacks. Stop then returns
// false, matching a callback that has been dispatched but is waiting for a lock.
// all also permits early delivery to test defensive deadline/generation guards.
func (c *Clock) Take(all bool) []func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var timers []*Timer
	for timer := range c.timers {
		if all || !c.now.Before(timer.deadline) {
			timers = append(timers, timer)
		}
	}
	sort.Slice(timers, func(i, j int) bool {
		if timers[i].deadline.Equal(timers[j].deadline) {
			return timers[i].sequence < timers[j].sequence
		}
		return timers[i].deadline.Before(timers[j].deadline)
	})
	callbacks := make([]func(), 0, len(timers))
	for _, timer := range timers {
		delete(c.timers, timer)
		callbacks = append(callbacks, timer.callback)
	}
	return callbacks
}
