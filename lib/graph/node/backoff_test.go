// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestBackoffLinearGrowth(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        spec.BackoffLinear,
		BaseDelayMs: 100,
		Jitter:      spec.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(cfg, 0, nil))
	require.Equal(t, 200, ComputeDelay(cfg, 1, nil))
	require.Equal(t, 300, ComputeDelay(cfg, 2, nil))
	require.Equal(t, 400, ComputeDelay(cfg, 3, nil))
}

func TestBackoffExponentialGrowth(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        spec.BackoffExponential,
		BaseDelayMs: 100,
		Jitter:      spec.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(cfg, 0, nil))
	require.Equal(t, 200, ComputeDelay(cfg, 1, nil))
	require.Equal(t, 400, ComputeDelay(cfg, 2, nil))
	require.Equal(t, 800, ComputeDelay(cfg, 3, nil))
}

func TestBackoffJitterPlusMinusStaysInRange(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        spec.BackoffLinear,
		BaseDelayMs: 1000,
		Jitter:      spec.JitterPlusMinus,
	}
	base := 1000.0
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
		Kind:        spec.BackoffLinear,
		BaseDelayMs: 1000,
		Jitter:      spec.JitterNone,
		MaxDelayMs:  5000,
	}
	require.Equal(t, 5000, ComputeDelay(cfg, 10, nil))
}

func TestBackoffZeroMaxUsesUnbounded(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        spec.BackoffExponential,
		BaseDelayMs: 100,
		Jitter:      spec.JitterNone,
		MaxDelayMs:  0,
	}
	require.Equal(t, math.MaxInt32, ComputeDelay(cfg, 100, nil))
	require.Equal(t, 100*(1<<20), ComputeDelay(cfg, 20, nil))
}

func TestBackoffNegativeAttemptIsTreatedAsZero(t *testing.T) {
	cfg := BackoffConfig{
		Kind:        spec.BackoffLinear,
		BaseDelayMs: 100,
		Jitter:      spec.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(cfg, -1, nil))
	require.Equal(t, 100, ComputeDelay(cfg, -100, nil))

	expCfg := BackoffConfig{
		Kind:        spec.BackoffExponential,
		BaseDelayMs: 100,
		Jitter:      spec.JitterNone,
	}
	require.Equal(t, 100, ComputeDelay(expCfg, -5, nil))
}
