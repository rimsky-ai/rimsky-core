// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package shared

import (
	"context"
	"runtime"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

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

func (c *ControllableClock) Advance(d time.Duration) {
	c.advanceTo(c.Now().Add(d))
}

func (c *ControllableClock) SetNow(t time.Time) {
	c.advanceTo(t)
}

func (c *ControllableClock) Tick() {
	for i := 0; i < 8; i++ {
		runtime.Gosched()
	}
}

func (c *ControllableClock) advanceTo(target time.Time) {
	const maxRounds = 1000
	for rounds := 0; rounds < maxRounds; rounds++ {
		c.mu.Lock()
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
	for _, p := range due {
		close(p.done)
	}
}

type AutoAdvanceClock struct {
	mu sync.Mutex
	t  time.Time
}

func NewAutoAdvanceClock(start time.Time) *AutoAdvanceClock {
	return &AutoAdvanceClock{t: start}
}

func (c *AutoAdvanceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *AutoAdvanceClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.t = c.t.Add(d)
	}
	return nil
}
