// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"github.com/fallguy/rimsky/foundation/shared"
)

// applyAttributeOverrides composes the post-resolution attribute bag
// with the two runtime override layers (L3 + L4 in the four-layer
// merge documented in concept:attribute):
//
//	resolved    ← source-resolved + static-default values (already merged
//	              against the executor's schema and the template's L1+L2
//	              by substituteAttributesSchema)
//	by_executor ← instance.attribute_overrides.by_executor[<executor>]
//	by_node     ← instance.attribute_overrides.by_node[<node>]
//
// More specific wins; L4 over L3 over resolved. Inputs are not mutated.
// Returns the merged map, always a freshly-allocated clone, so callers
// may mutate it freely.
//
// Wire shape of overrides (validated at instance-create by the
// control-api):
//
//	{
//	  "by_executor": {"<executor-name>": { ...attribute-fragment... }},
//	  "by_node":     {"<node-name>":     { ...attribute-fragment... }}
//	}
//
// The merge is shape-blind: rimsky inspects only the routing keys
// (`by_executor`, `by_node`, executor-name, node-name) — never the
// fragment values themselves. The fragment values flow into the
// dispatched attribute bag verbatim, covered by the structural-inertness
// discipline (concept:inertness).
//
// Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Override layering".
//
// @concept: attribute
func applyAttributeOverrides(
	resolved map[string]any, // post-substitution + post-static-default attribute bag
	overrides map[string]any, // instance.attribute_overrides
	executor string,
	nodeName string,
	logger shared.Logger,
) map[string]any {
	merged := any(shared.DeepMergeJSON(resolved, nil))
	if len(overrides) == 0 {
		if m, ok := merged.(map[string]any); ok {
			return m
		}
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
		// nor by_node[<nodeName>] resolved a fragment for this dispatch.
		// The pre-merge resolved bag is the final result.
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
	// silent-no-op leaves a trace rather than vanishing.
	if logger != nil {
		logger.Warn("applyAttributeOverrides: merge produced non-map root; falling back to resolved",
			"executor", executor,
			"node_name", nodeName)
	}
	cloned, _ := shared.DeepMergeJSON(resolved, nil).(map[string]any)
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
