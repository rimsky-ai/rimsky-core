// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"github.com/fallguy/rimsky/foundation/shared"
)

// applyUserdataOverrides composes the per-dispatch userdata Struct
// payload by deep-merging per-instance overrides on top of per-template
// userdata in order of increasing specificity:
//
//	base ← template's per-node userdata (acq.NodeDef.Userdata)
//	     ← overrides.by_executor[<node's executor>]    (less specific)
//	     ← overrides.by_node[<node's name>]            (most specific)
//
// Inputs are not mutated. Returns the merged map. The returned map is
// always a freshly-allocated clone (even on the no-overrides fast path),
// so callers may mutate it freely without affecting the captured
// per-template userdata.
//
// Wire shape of overrides (validated at instance-create by the
// control-api):
//
//	{
//	  "by_executor": {"<executor-name>": { ...userdata-fragment... }},
//	  "by_node":     {"<node-name>":     { ...userdata-fragment... }}
//	}
//
// Per @blessed-invariant 11 the merge is shape-blind: rimsky inspects
// only the routing-keys (`by_executor`, `by_node`, executor-name,
// node-name) — never the userdata fragments themselves. The fragment
// values are forwarded to the executor verbatim.
func applyUserdataOverrides(
	base map[string]any,
	overrides map[string]any,
	executor string,
	nodeName string,
	logger shared.Logger,
) map[string]any {
	if len(overrides) == 0 {
		// Fast path: no overrides. Clone via DeepMergeJSON(base, nil) so
		// the returned map is owned and mutable, matching the contract on
		// the override-applied path (DeepMergeJSON always clones). Cost
		// is negligible — userdata fragments are small.
		cloned, _ := shared.DeepMergeJSON(base, nil).(map[string]any)
		return cloned
	}
	merged := any(base)
	applied := false
	if frag, ok := lookupFragment(overrides, "by_executor", executor); ok {
		merged = shared.DeepMergeJSON(merged, frag)
		applied = true
	}
	if frag, ok := lookupFragment(overrides, "by_node", nodeName); ok {
		merged = shared.DeepMergeJSON(merged, frag)
		applied = true
	}
	if !applied {
		// Overrides was non-empty, but neither by_executor[<executor>]
		// nor by_node[<nodeName>] resolved a fragment for this dispatch
		// (e.g. all entries target a different executor or node). No
		// merge happened — return a clone of base, matching the no-
		// overrides fast-path semantics. Without this short-circuit,
		// `merged` would be `any(base)`; if base is nil, the type
		// assertion below would fail and erroneously fire the "merge
		// produced non-map root" Warn.
		cloned, _ := shared.DeepMergeJSON(base, nil).(map[string]any)
		return cloned
	}
	if m, ok := merged.(map[string]any); ok {
		return m
	}
	// `lookupFragment` guarantees fragments are `map[string]any`, and
	// `DeepMergeJSON` of two maps always returns a map, so this branch
	// is unreachable in steady state — only a future bug in DeepMergeJSON
	// or lookupFragment could land us here. Surface a Warn so the
	// silent-no-op leaves a trace rather than vanishing into the per-
	// template defaults.
	if logger != nil {
		logger.Warn("applyUserdataOverrides: merge produced non-map root; falling back to base",
			"executor", executor,
			"node_name", nodeName)
	}
	cloned, _ := shared.DeepMergeJSON(base, nil).(map[string]any)
	return cloned
}

// lookupFragment returns overrides[key][subkey] if both lookups produce
// an object. Returns (nil, false) for any miss or shape mismatch.
func lookupFragment(overrides map[string]any, key, subkey string) (map[string]any, bool) {
	raw, ok := overrides[key]
	if !ok {
		return nil, false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	frag, ok := m[subkey]
	if !ok {
		return nil, false
	}
	fm, ok := frag.(map[string]any)
	if !ok {
		return nil, false
	}
	return fm, true
}
