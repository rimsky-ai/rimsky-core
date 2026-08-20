// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package shared

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/awaited"
	"github.com/stretchr/testify/require"
)

func TestClockSystemClockNow(t *testing.T) {
	before := time.Now()
	got := SystemClock{}.Now()
	after := time.Now()
	require.False(t, got.Before(before), "SystemClock.Now() read %s, before the wall clock's %s", got, before)
	require.False(t, got.After(after), "SystemClock.Now() read %s, after the wall clock's %s", got, after)
}

func TestClockSystemClockSleepRespectsCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, SystemClock{}.Sleep(ctx, time.Hour), context.Canceled)
}

func TestClockControllableClockNow(t *testing.T) {
	start := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	c := NewControllableClock(start)
	require.Equal(t, start, c.Now())
}

func TestClockControllableClockSleepBlocksUntilAdvance(t *testing.T) {
	start := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	c := NewControllableClock(start)

	var woke atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := c.Sleep(context.Background(), 50*time.Millisecond)
		require.NoError(t, err)
		woke.Store(true)
	}()

	awaited.Until(t, "the sleeper to register its pending deadline", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.pending) == 1
	})
	require.False(t, woke.Load(), "sleeper should still be blocked")

	c.Advance(50 * time.Millisecond)
	wg.Wait()
	require.True(t, woke.Load())
	require.Equal(t, start.Add(50*time.Millisecond), c.Now())
}

func TestClockControllableClockAdvanceWakesEverySleeperItsDeadlinePassedAndNoOther(t *testing.T) {
	start := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	c := NewControllableClock(start)

	var wg sync.WaitGroup
	sleepers := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	wokeAt := make([]atomic.Pointer[time.Time], len(sleepers))

	for i, d := range sleepers {
		wg.Add(1)
		go func(idx int, dur time.Duration) {
			defer wg.Done()
			if err := c.Sleep(context.Background(), dur); err != nil {
				return
			}
			observed := c.Now()
			wokeAt[idx].Store(&observed)
		}(i, d)
	}

	awaited.Until(t, "all three sleepers to register their pending deadlines", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.pending) == 3
	})

	c.Advance(25 * time.Millisecond)
	awaited.Until(t, "the two sleepers the advance passed to wake",
		func() bool { return wokeAt[0].Load() != nil && wokeAt[1].Load() != nil })

	require.Nil(t, wokeAt[2].Load(), "a sleeper whose deadline the advance did not reach must stay blocked")
	for _, idx := range []int{0, 1} {
		require.False(t, wokeAt[idx].Load().Before(start.Add(sleepers[idx])),
			"sleeper %d woke at %s, before its %s deadline", idx, wokeAt[idx].Load(), start.Add(sleepers[idx]))
	}

	c.Advance(10 * time.Millisecond)
	wg.Wait()
	require.NotNil(t, wokeAt[2].Load())
	require.False(t, wokeAt[2].Load().Before(start.Add(sleepers[2])))
}

func TestClockControllableClockSleepRespectsCtx(t *testing.T) {
	start := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	c := NewControllableClock(start)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Sleep(ctx, time.Hour)
	}()

	awaited.Until(t, "the sleeper to register its pending deadline", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.pending) == 1
	})
	cancel()

	require.ErrorIs(t, <-errCh, context.Canceled)
}
