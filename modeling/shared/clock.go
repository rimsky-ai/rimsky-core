package shared

import (
	"context"
	"runtime"
	"sort"
	"sync"
	"time"
)

// Clock abstracts time sourcing and sleeping so cell-graph components can be
// driven deterministically in tests.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// SystemClock is the production Clock backed by the runtime wall clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type pendingSleep struct {
	due  time.Time
	done chan struct{}
}

// ControllableClock is a deterministic in-memory Clock for tests. Callers drive
// time forward with Advance or SetNow; pending sleeps resolve in deadline
// order, and microtask yields let chained sleepers register their next sleep
// before Advance returns.
type ControllableClock struct {
	mu      sync.Mutex
	t       time.Time
	pending []*pendingSleep
}

func NewControllableClock(start time.Time) *ControllableClock {
	return &ControllableClock{t: start}
}

func (c *ControllableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ControllableClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	p := &pendingSleep{due: c.t.Add(d), done: make(chan struct{})}
	c.pending = append(c.pending, p)
	c.mu.Unlock()

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		c.cancelPending(p)
		return ctx.Err()
	}
}

func (c *ControllableClock) cancelPending(target *pendingSleep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.pending[:0]
	for _, p := range c.pending {
		if p != target {
			out = append(out, p)
		}
	}
	c.pending = out
}

// Advance moves the clock forward by d, resolving pending sleeps in deadline
// order and yielding microtasks between each so chained sleepers can register
// their next sleep before the advance completes.
func (c *ControllableClock) Advance(d time.Duration) {
	c.advanceTo(c.nowLocked().Add(d))
}

// SetNow replaces the clock's time and fires pending sleeps whose deadlines
// are now reached.
func (c *ControllableClock) SetNow(t time.Time) {
	c.advanceTo(t)
}

// Tick yields microtasks so goroutines that had sleeps resolved can run their
// follow-up code (including enqueuing fresh sleeps) before the caller
// continues.
func (c *ControllableClock) Tick() {
	for i := 0; i < 8; i++ {
		runtime.Gosched()
	}
}

func (c *ControllableClock) nowLocked() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ControllableClock) advanceTo(target time.Time) {
	const maxRounds = 1000
	for rounds := 0; rounds < maxRounds; rounds++ {
		c.mu.Lock()
		// Find earliest pending deadline at or before target.
		var nextDue *time.Time
		for _, p := range c.pending {
			if !p.due.After(target) {
				if nextDue == nil || p.due.Before(*nextDue) {
					d := p.due
					nextDue = &d
				}
			}
		}
		if nextDue == nil {
			c.t = target
			c.mu.Unlock()
			c.Tick()
			// If chained code registered a new sleep <= target during those
			// yields, loop to handle it.
			c.mu.Lock()
			hasDue := false
			for _, p := range c.pending {
				if !p.due.After(target) {
					hasDue = true
					break
				}
			}
			c.mu.Unlock()
			if hasDue {
				continue
			}
			return
		}
		c.t = *nextDue
		c.flushDueLocked()
		c.mu.Unlock()
		c.Tick()
	}
	panic("ControllableClock.Advance: pending sleeps did not stabilize after 1000 rounds")
}

// flushDueLocked resolves every pending sleep whose deadline is <= c.t, in
// deadline order. Caller must hold c.mu.
func (c *ControllableClock) flushDueLocked() {
	due := make([]*pendingSleep, 0, len(c.pending))
	remaining := c.pending[:0]
	for _, p := range c.pending {
		if !p.due.After(c.t) {
			due = append(due, p)
		} else {
			remaining = append(remaining, p)
		}
	}
	c.pending = remaining
	sort.SliceStable(due, func(i, j int) bool { return due[i].due.Before(due[j].due) })
	for _, p := range due {
		close(p.done)
	}
}
