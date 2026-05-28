// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClockSystemClockNow(t *testing.T) {
	got := SystemClock{}.Now()
	require.WithinDuration(t, time.Now(), got, time.Second)
}

func TestClockSystemClockSleepRespectsCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := SystemClock{}.Sleep(ctx, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 200*time.Millisecond)
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

	// Give goroutine time to register the pending sleep.
	for i := 0; i < 100 && func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.pending) == 0
	}(); i++ {
		time.Sleep(time.Millisecond)
	}
	require.False(t, woke.Load(), "sleeper should still be blocked")

	c.Advance(50 * time.Millisecond)
	wg.Wait()
	require.True(t, woke.Load())
	require.Equal(t, start.Add(50*time.Millisecond), c.Now())
}

func TestClockControllableClockMultipleSleepersResolveInDeadlineOrder(t *testing.T) {
	start := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	c := NewControllableClock(start)

	var mu sync.Mutex
	order := []int{}
	var wg sync.WaitGroup
	sleepers := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	wokeFlags := make([]atomic.Bool, len(sleepers))

	for i, d := range sleepers {
		wg.Add(1)
		go func(idx int, dur time.Duration) {
			defer wg.Done()
			if err := c.Sleep(context.Background(), dur); err != nil {
				return
			}
			mu.Lock()
			order = append(order, idx)
			mu.Unlock()
			wokeFlags[idx].Store(true)
		}(i, d)
	}

	// Wait until all three sleeps have registered.
	for i := 0; i < 200; i++ {
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	c.Advance(25 * time.Millisecond)
	// Yield so the two resolved sleepers can append to `order`.
	for i := 0; i < 200; i++ {
		if wokeFlags[0].Load() && wokeFlags[1].Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	require.Equal(t, []int{0, 1}, order)
	mu.Unlock()
	require.False(t, wokeFlags[2].Load())

	c.Advance(10 * time.Millisecond)
	wg.Wait()
	require.True(t, wokeFlags[2].Load())
}

func TestClockControllableClockSleepRespectsCtx(t *testing.T) {
	start := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	c := NewControllableClock(start)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Sleep(ctx, time.Hour)
	}()

	// Ensure sleep registered before cancelling.
	for i := 0; i < 100; i++ {
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Sleep did not return after ctx cancel")
	}
}
