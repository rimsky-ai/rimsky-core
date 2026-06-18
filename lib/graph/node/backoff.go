// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"math"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type BackoffConfig struct {
	Kind        spec.BackoffKind
	BaseDelayMs int
	Jitter      spec.JitterKind
	MaxDelayMs int
}

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
		base *= 0.5 + rng()
	}
	if base < 0 {
		base = 0
	}
	maxMs := cfg.MaxDelayMs
	if maxMs <= 0 {
		maxMs = math.MaxInt32
	}
	if base > float64(maxMs) {
		base = float64(maxMs)
	}
	return int(base)
}
