// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Three-deadline resolution helpers (TD-three-dispatch-deadlines).
//
// Each helper folds a per-node template value (string-form duration
// from code:lib/foundation/spec/template.go::TemplateNodeDef) over a
// deployment-level default (time.Duration carried on RunArgs) and a
// built-in fallback. The three knobs:
//
//   - sync_rpc_deadline: bounds the supervisor's outgoing unary
//     Execute RPC. Default 30s.
//   - max_quiet_period: bounds an async-mode dispatch's time without
//     bumping last_progress_at. Default 0 = disabled (operator opt-in).
//   - max_runtime: bounds total wall-clock from dispatch claim to
//     terminal. Default 0 = disabled (operator opt-in).
//
// Resolution order, repeated for each knob:
//
//  1. If the per-node string parses to a duration, use it directly.
//  2. Otherwise, if the deployment default is non-zero, use it.
//  3. Otherwise, fall back to the built-in default.
//
// A literal "0s" on the per-node value disables the cap for that
// node — explicit by construction, distinct from empty (use deployment
// default).
//
// @concept: dispatch-deadlines
// @decision: dispatch-deadlines

package runtime

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @constraint: built-in defaults applied when neither the per-node
// value nor the deployment default specifies otherwise.
const (
	// @deliberate: 30s matches the plan-time recommendation in
	// spec:2026-06-16-executor-protocol-coherence-design — generous
	// enough that a slow handler finishes, tight enough that a hung
	// executor surfaces fast.
	defaultSyncRPCDeadline = 30 * time.Second
	// @deliberate: disabled by default — operator opts in (per spec
	// "default 0 = disabled"). An async dispatch with no quiet-period
	// cap settles only via its own terminal or via max_runtime; the
	// orphan-reaper does not act on connection-state semantics for
	// async (those are sync-only) per concept:orphan-reaper.
	defaultMaxQuietPeriod = time.Duration(0)
	// @deliberate: disabled by default — operator-opt-in safety net,
	// not on by accident.
	defaultMaxRuntime = time.Duration(0)
)

// resolveSyncRPCDeadline folds the per-node SyncRPCDeadline over the
// deployment default and the built-in fallback. Returns 0 when all
// three resolve to "disabled" (caller skips the deadline wrap).
//
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

// resolveMaxQuietPeriod folds per-node over deployment default over
// built-in.
//
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

// resolveMaxRuntime folds per-node over deployment default over
// built-in. Note: built-in default is 0 (disabled) for max_runtime —
// unlike the other two, the operator must opt in.
//
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

// parseDispatchDeadline parses a TemplateNodeDef duration string. A
// non-parseable value returns (0, false) — caller falls back to the
// deployment default. A negative parse returns (0, false) as well
// (the validator should have rejected these at template-registration
// time; defensive runtime parse).
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

// computeEffectiveDeadlineSecs resolves the per-row dispatch deadlines
// for the per-row denormalization onto
// col:rimsky_node_runs.effective_max_quiet_period_seconds and
// col:rimsky_node_runs.effective_max_runtime_seconds. Returns (nil, nil)
// for the disabled-0 case so the column stays NULL and
// code:SweepExecutorDeadlines's per-row guard treats it as "no cap"
// without a magic zero check.
//
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
