// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"math"
	"testing"

	"github.com/fallguy/rimsky/graph/shared"
	"github.com/stretchr/testify/require"
)

func TestBackoffLinearGrowth(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        shared.BackoffLinear,
		BaseDelayMs: 100,
		Jitter:      shared.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(cfg, 0, nil))
	require.Equal(t, 200, ComputeDelay(cfg, 1, nil))
	require.Equal(t, 300, ComputeDelay(cfg, 2, nil))
	require.Equal(t, 400, ComputeDelay(cfg, 3, nil))
}

func TestBackoffExponentialGrowth(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        shared.BackoffExponential,
		BaseDelayMs: 100,
		Jitter:      shared.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(cfg, 0, nil))
	require.Equal(t, 200, ComputeDelay(cfg, 1, nil))
	require.Equal(t, 400, ComputeDelay(cfg, 2, nil))
	require.Equal(t, 800, ComputeDelay(cfg, 3, nil))
}

func TestBackoffJitterPlusMinusStaysInRange(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        shared.BackoffLinear,
		BaseDelayMs: 1000,
		Jitter:      shared.JitterPlusMinus,
	}
	base := 1000.0 // attemptIndex=0 → base * 1
	// Deterministic pseudo-random sequence covering [0.0, 1.0).
	var i int
	values := []float64{0.0, 0.1, 0.25, 0.333, 0.5, 0.6, 0.75, 0.9, 0.9999}
	rng := func() float64 {
		v := values[i%len(values)]
		i++
		return v
	}
	for k := 0; k < 1000; k++ {
		got := ComputeDelay(cfg, 0, rng)
		require.GreaterOrEqual(t, float64(got), 0.5*base)
		require.Less(t, float64(got), 1.5*base)
	}
}

func TestBackoffMaxClamp(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        shared.BackoffLinear,
		BaseDelayMs: 1000,
		Jitter:      shared.JitterNone,
		MaxDelayMs:  5000,
	}
	// attemptIndex=10 → 1000 * 11 = 11000, clamped to 5000.
	require.Equal(t, 5000, ComputeDelay(cfg, 10, nil))
}

func TestBackoffZeroMaxUsesUnbounded(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        shared.BackoffExponential,
		BaseDelayMs: 100,
		Jitter:      shared.JitterNone,
		MaxDelayMs:  0,
	}
	// attemptIndex=20 → 100 * 2^20 = 104857600, below MaxInt32, but repeating
	// grows past it. Verify we get clamped to MaxInt32 for a huge attempt.
	require.Equal(t, math.MaxInt32, ComputeDelay(cfg, 100, nil))
	// attemptIndex=20 is below MaxInt32, so it should pass through unclamped.
	require.Equal(t, 100*(1<<20), ComputeDelay(cfg, 20, nil))
}

func TestBackoffNegativeAttemptIsTreatedAsZero(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        shared.BackoffLinear,
		BaseDelayMs: 100,
		Jitter:      shared.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(cfg, -1, nil))
	require.Equal(t, 100, ComputeDelay(cfg, -100, nil))

	expCfg := BackoffConfig{
		Kind:        shared.BackoffExponential,
		BaseDelayMs: 100,
		Jitter:      shared.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(expCfg, -5, nil))
}
