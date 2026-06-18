// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: dispatch-deadlines
// @decision: dispatch-deadlines

package runtime

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const (
	defaultSyncRPCDeadline = 30 * time.Second
	defaultMaxQuietPeriod = time.Duration(0)
	defaultMaxRuntime = time.Duration(0)
)

// @concept: dispatch-deadlines
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

// @concept: dispatch-deadlines
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

// @concept: dispatch-deadlines
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

// @concept: dispatch-deadlines
// @decision: dispatch-deadlines
func computeEffectiveDeadlineSecs(node *spec.TemplateNodeDef, quietDefault, runtimeDefault time.Duration) (*int, *int) {
	quiet := resolveMaxQuietPeriod(node, quietDefault)
	runtime := resolveMaxRuntime(node, runtimeDefault)
	var quietPtr, runtimePtr *int
	if quiet > 0 {
		s := int(quiet.Seconds())
		if s > 0 {
			quietPtr = &s
		}
	}
	if runtime > 0 {
		s := int(runtime.Seconds())
		if s > 0 {
			runtimePtr = &s
		}
	}
	return quietPtr, runtimePtr
}
