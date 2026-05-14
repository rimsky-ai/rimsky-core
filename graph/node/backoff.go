// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"math"

	"github.com/fallguy/rimsky/foundation/spec"
)

type BackoffConfig struct {
	Kind        spec.BackoffKind
	BaseDelayMs int
	Jitter      spec.JitterKind
	MaxDelayMs  int // 0 or negative = no max (treated as math.MaxInt32)
}

// ComputeDelay returns the delay in ms for a given attempt index (0-based).
//
//	linear:      base * (attemptIndex + 1)
//	exponential: base * 2^attemptIndex
//
// Jitter:
//
//	plus_minus: multiply by uniform random in [0.5, 1.5) (rng provides the
//	            random in [0.0, 1.0); computed as 0.5 + rng()).
//
// Then clamp to MaxDelayMs if > 0.
func ComputeDelay(cfg BackoffConfig, attemptIndex int, rng func() float64) int {
	if attemptIndex < 0 {
		attemptIndex = 0
	}
	var base float64
	switch cfg.Kind {
	case spec.BackoffLinear:
		base = float64(cfg.BaseDelayMs) * float64(attemptIndex+1)
	case spec.BackoffExponential:
		base = float64(cfg.BaseDelayMs) * math.Pow(2, float64(attemptIndex))
	default:
		base = float64(cfg.BaseDelayMs)
	}
	if cfg.Jitter == spec.JitterPlusMinus && rng != nil {
		base *= 0.5 + rng() // in [0.5, 1.5)
	}
	if base < 0 {
		base = 0
	}
	maxMs := cfg.MaxDelayMs
	if maxMs <= 0 {
		maxMs = math.MaxInt32 // effectively unbounded for our use case
	}
	if base > float64(maxMs) {
		base = float64(maxMs)
	}
	return int(base)
}
