// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrencyGate_DeniesAcquireAtLimit(t *testing.T) {
	t.Parallel()
	g := newConcurrencyGate(2)

	require.True(t, g.tryAcquire())
	require.True(t, g.tryAcquire())
	require.False(t, g.tryAcquire(),
		"tryAcquire at the concurrency limit must early-return false so the supervisor claim loop skips Queue.Claim entirely")
	require.Equal(t, 2, g.activeCount())

	g.release()
	require.True(t, g.tryAcquire(), "a released slot must be re-acquirable")
	require.Equal(t, 2, g.activeCount())
}

func TestConcurrencyGate_LimitFloorsAtOne(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{-3, 0, 1} {
		g := newConcurrencyGate(limit)
		require.True(t, g.tryAcquire(), "limit %d must floor to 1, admitting a single run", limit)
		require.False(t, g.tryAcquire(), "limit %d must floor to 1, denying a second concurrent run", limit)
	}
}

func TestConcurrencyGate_ReleaseWithoutAcquirePanics(t *testing.T) {
	t.Parallel()
	g := newConcurrencyGate(1)
	require.Panics(t, func() { g.release() },
		"an unpaired release is a supervisor accounting bug and must fail loudly, not silently widen the gate")
}

func TestConcurrencyGate_ConcurrentAcquireReleaseNeverExceedsLimit(t *testing.T) {
	t.Parallel()
	const (
		limit      = 4
		goroutines = 16
		iterations = 200
	)
	g := newConcurrencyGate(limit)

	var inFlight atomic.Int32
	var exceeded atomic.Bool
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if !g.tryAcquire() {
					continue
				}
				if inFlight.Add(1) > limit {
					exceeded.Store(true)
				}
				inFlight.Add(-1)
				g.release()
			}
		}()
	}
	wg.Wait()

	require.False(t, exceeded.Load(),
		"the gate admitted more than %d concurrent holders", limit)
	require.Equal(t, 0, g.activeCount(),
		"every successful tryAcquire paired with a release must drain the gate back to zero")
}
