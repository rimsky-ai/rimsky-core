// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"github.com/fallguy/rimsky/foundation/shared"
)

// applyUserdataOverrides composes the per-dispatch userdata Struct
// payload by deep-merging four layers in order of increasing
// specificity:
//
//	templateDefaults ← template.defaults.userdata.by_executor[<executor>]
//	base             ← node.userdata
//	by_executor      ← instance.userdata_overrides.by_executor[<executor>]
//	by_node          ← instance.userdata_overrides.by_node[<node>]
//
// More specific wins; operator-level overrides win over template-author
// defaults. Inputs are not mutated. Returns the merged map, always a
// freshly-allocated clone, so callers may mutate it freely.
//
// Wire shape of overrides (validated at instance-create by the
// control-api):
//
//	{
//	  "by_executor": {"<executor-name>": { ...userdata-fragment... }},
//	  "by_node":     {"<node-name>":     { ...userdata-fragment... }}
//	}
//
// `templateDefaults` is the already-routed by-executor fragment from
// `TemplateSpec.Defaults.Userdata.ByExecutor[executor]`; the caller does
// the routing key lookup so this function deals with fragments only.
//
// Per @blessed-invariant 11 the merge is shape-blind: rimsky inspects
// only the routing-keys (`by_executor`, `by_node`, executor-name,
// node-name) — never the userdata fragments themselves. The fragment
// values are forwarded to the executor verbatim.
//
// @concept: userdata
func applyUserdataOverrides(
	templateDefaults map[string]any, // template.defaults.userdata.by_executor[executor]; may be nil
	base map[string]any, // node.userdata
	overrides map[string]any, // instance.userdata_overrides
	executor string,
	nodeName string,
	logger shared.Logger,
) map[string]any {
	// Layer 1+2: fold template-author defaults underneath node.userdata.
	// DeepMergeJSON(nil, x) returns a clone of x, so this works for any
	// combination of nil/non-nil inputs.
	var merged any
	if templateDefaults != nil {
		merged = shared.DeepMergeJSON(templateDefaults, base)
	} else {
		// No template defaults — clone base for the same reason as the
		// pre-extension fast path.
		merged = shared.DeepMergeJSON(base, nil)
	}

	if len(overrides) == 0 {
		// No per-instance overrides; the merged-defaults+base layer is
		// the final result.
		if m, ok := merged.(map[string]any); ok {
			return m
		}
		// Defensive: both templateDefaults and base were nil — return an
		// empty owned map (matches the prior contract).
		return map[string]any{}
	}

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
		// (e.g. all entries target a different executor or node). The
		// merged-defaults+base layer is still the final result.
		if m, ok := merged.(map[string]any); ok {
			return m
		}
		return map[string]any{}
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
