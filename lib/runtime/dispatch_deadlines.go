// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: three-dispatch-deadlines

package runtime

import (
	"math"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const (
	defaultSyncRPCDeadline = 30 * time.Second
	defaultMaxQuietPeriod  = time.Duration(0)
	defaultMaxRuntime      = time.Duration(0)
)

// @decision: three-dispatch-deadlines
func resolveSyncRPCDeadline(node *spec.TemplateNodeDef, deploymentDefault time.Duration) time.Duration {
	if node != nil && node.SyncRPCDeadline != "" {
		if d, ok := parseDispatchDeadline(node.SyncRPCDeadline); ok {
			return d
		}
	}
	if deploymentDefault > 0 {
		return deploymentDefault
	}
	return defaultSyncRPCDeadline
}

// @decision: three-dispatch-deadlines
func resolveMaxQuietPeriod(node *spec.TemplateNodeDef, deploymentDefault time.Duration) time.Duration {
	if node != nil && node.MaxQuietPeriod != "" {
		if d, ok := parseDispatchDeadline(node.MaxQuietPeriod); ok {
			return d
		}
	}
	if deploymentDefault > 0 {
		return deploymentDefault
	}
	return defaultMaxQuietPeriod
}

// @decision: three-dispatch-deadlines
func resolveMaxRuntime(node *spec.TemplateNodeDef, deploymentDefault time.Duration) time.Duration {
	if node != nil && node.MaxRuntime != "" {
		if d, ok := parseDispatchDeadline(node.MaxRuntime); ok {
			return d
		}
	}
	if deploymentDefault > 0 {
		return deploymentDefault
	}
	return defaultMaxRuntime
}

func parseDispatchDeadline(s string) (time.Duration, bool) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	if d < 0 {
		return 0, false
	}
	return d, true
}

// @decision: three-dispatch-deadlines
func computeEffectiveDeadlineSecs(node *spec.TemplateNodeDef, quietDefault, runtimeDefault time.Duration) (*int, *int) {
	quiet := resolveMaxQuietPeriod(node, quietDefault)
	runtime := resolveMaxRuntime(node, runtimeDefault)
	var quietPtr, runtimePtr *int
	if quiet > 0 {
		s := roundToPositiveSeconds(quiet)
		quietPtr = &s
	}
	if runtime > 0 {
		s := roundToPositiveSeconds(runtime)
		runtimePtr = &s
	}
	return quietPtr, runtimePtr
}

// @decision: three-dispatch-deadlines
func roundToPositiveSeconds(d time.Duration) int {
	s := int(math.Round(d.Seconds()))
	if s < 1 {
		s = 1
	}
	return s
}
