// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/fallguyconsulting/rimsky/foundation/matcher"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// applyAttributeOverrides composes the post-resolution attribute bag
// with the three runtime override layers (L3, L4, and L5 in the merge
// documented in concept:attribute):
//
//	resolved    ← source-resolved + static-default values (already merged
//	              against the executor's schema and the template's L1+L2
//	              by substituteAttributesSchema)
//	by_executor ← instance.attribute_overrides.by_executor[<executor>]
//	by_node     ← instance.attribute_overrides.by_node[<node>]
//	by_match    ← instance.attribute_overrides.by_match[].overlay, for
//	              every entry whose matcher predicate evaluates true
//	              against the dispatch context.
//
// More specific wins; L5 over L4 over L3 over resolved. Within L5,
// later entries win on conflict (declaration order). Inputs are not
// mutated. Returns the merged map plus the list of by_match entry
// indices whose matchers fired — the supervisor's
// IncrementAttributeOverrideMatchCounts call increments those
// per-entry counters synchronously after the merge returns.
//
// Wire shape of overrides (validated at instance-create by the
// control-api):
//
//	{
//	  "by_executor": {"<executor-name>": { ...attribute-fragment... }},
//	  "by_node":     {"<node-name>":     { ...attribute-fragment... }},
//	  "by_match":    [
//	    {
//	      "matcher": {
//	        "node_type": "<name>",         // optional
//	        "executor":  "<name>",         // optional
//	        "graph":     "<name>",         // optional ("main" or sub-graph name)
//	        "child_key": "<value>",        // optional
//	        "attrs":     {"<dotted.path>": <primitive>, ...} // optional
//	      },
//	      "overlay": { ...attribute-fragment... }
//	    },
//	    ...
//	  ]
//	}
//
// The L5 matcher is equality-only over a closed key set and AND-joins
// across present keys. Missing matcher keys are wildcards; an empty
// matcher ({}) fires for every dispatch. The matcher reads from the
// *post-L4* snapshot — overlays applied by earlier L5 entries are not
// visible to later L5 matchers.
//
// L3/L4 are shape-blind on the routing keys (`by_executor`, `by_node`,
// executor-name, node-name); L5 inspects only the matcher's predicate
// values per concept:inertness (sanctioned read site for attribute
// values). The fragment values themselves remain inert — they flow
// into the dispatched attribute bag verbatim.
//
// Per spec
// .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
// §"Dispatch evaluation".
//
// @concept: attribute
func applyAttributeOverrides(
	resolved map[string]any, // post-substitution + post-static-default attribute bag
	overrides map[string]any, // single blob: by_executor + by_node + by_match
	executor string,
	nodeName string,
	graph string, // "main" (spec.MainGraphName) or sub-graph name
	childKey string, // "" for non-fan-out dispatches
	logger shared.Logger,
) (merged map[string]any, matched []int) {
	mergedAny := any(shared.DeepMergeJSON(resolved, nil))
	if len(overrides) == 0 {
		if m, ok := mergedAny.(map[string]any); ok {
			return m, nil
		}
		return map[string]any{}, nil
	}

	if frag, ok := lookupFragment(overrides, "by_executor", executor); ok {
		mergedAny = shared.DeepMergeJSON(mergedAny, frag)
	}
	if frag, ok := lookupFragment(overrides, "by_node", nodeName); ok {
		mergedAny = shared.DeepMergeJSON(mergedAny, frag)
	}

	// Snapshot the post-L4 bag for the matcher. Per the design intent,
	// every L5 entry's matcher reads from the same snapshot so the
	// matchers are independent of L5 declaration order.
	matcherCtx, _ := mergedAny.(map[string]any)
	if matcherCtx == nil {
		matcherCtx = map[string]any{}
	}

	if entries, ok := lookupMatchList(overrides, logger); ok {
		for i, entry := range entries {
			if entry == nil {
				// Malformed per-entry shape: skipped+warned at extract
				// time by lookupMatchList. Preserve the original index
				// in `matched` only on successful evaluation, so a bad
				// entry is fully invisible to the counter path.
				continue
			}
			matcherMap, _ := entry["matcher"].(map[string]any)
			overlay, _ := entry["overlay"].(map[string]any)
			if !evaluateMatcher(matcherMap, executor, nodeName, graph, childKey, matcherCtx, logger, i) {
				continue
			}
			if overlay != nil {
				mergedAny = shared.DeepMergeJSON(mergedAny, overlay)
			}
			matched = append(matched, i)
		}
	}

	if m, ok := mergedAny.(map[string]any); ok {
		return m, matched
	}
	// `lookupFragment` guarantees fragments are `map[string]any`, and
	// `DeepMergeJSON` of two maps always returns a map, so this branch
	// is unreachable in steady state. Surface a Warn so the silent
	// no-op leaves a trace rather than vanishing.
	if logger != nil {
		logger.Warn("applyAttributeOverrides: merge produced non-map root; falling back to resolved",
			"executor", executor,
			"node_name", nodeName)
	}
	cloned, _ := shared.DeepMergeJSON(resolved, nil).(map[string]any)
	return cloned, matched
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

// lookupMatchList returns overrides["by_match"] coerced to
// []map[string]any. The slice has one slot per input entry so the
// per-entry index lines up with the matched-counter array on
// rimsky_instances.attribute_overrides_match_counts.
//
// Top-level shape problems (missing by_match key, by_match itself
// not an array) return (nil, false) and skip the L5 fold entirely.
//
// PER-ENTRY shape problems (a non-object item inside the array)
// degrade locally: that slot in the returned slice is nil, a Warn is
// emitted with the offending index, and the rest of the list is
// returned for normal evaluation. The caller skips nil slots.
//
// The all-or-nothing degradation of an earlier revision masked
// per-entry corruption — every entry's counter stayed at 0 even
// when only one entry was malformed. Per-entry degradation makes
// operators see exactly which slot is broken via the warn log + the
// surviving counters on the other slots.
//
// The validator at instance-create rejects malformed shapes; this
// runtime helper only sees malformed shapes on out-of-band
// persistence corruption.
func lookupMatchList(overrides map[string]any, logger shared.Logger) ([]map[string]any, bool) {
	raw, ok := overrides["by_match"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			if logger != nil {
				logger.Warn("applyAttributeOverrides: by_match entry has malformed shape; skipping",
					"entry_index", i)
			}
			continue // out[i] stays nil; caller skips.
		}
		out[i] = m
	}
	return out, true
}

// evaluateMatcher delegates to foundation/matcher.Evaluate, projecting
// the runtime's positional dispatch-identity arguments into
// matcher.Context. The matcher package owns the closed key set, the
// defensive unknown-key guard, the attribute-path walk, and the
// primitive-equality coercion (preserved verbatim from this file's
// previous home; concept:inertness sanctioned read site moved with the
// attrs branch).
//
// `entryIndex` is the matcher's position in the instance's `by_match`
// list; threaded through for the matcher package's unknown-key warn
// log so operators can find the malformed slot. `logger` may be nil —
// the helper degrades to silent skip in that case.
func evaluateMatcher(
	m map[string]any,
	executor, nodeName, graph, childKey string,
	bag map[string]any,
	logger shared.Logger,
	entryIndex int,
) bool {
	return matcher.Evaluate(matcher.Matcher(m), matcher.Context{
		Executor:     executor,
		NodeType:     nodeName,
		Graph:        graph,
		ChildKey:     childKey,
		AttributeBag: bag,
	}, logger, entryIndex)
}

// matchCounterPersist is the minimal slice of persistence.Tables that
// incrementMatchCountersAfterMerge actually uses. Declared inline so
// the helper can be exercised against a tiny test double without
// re-implementing every method on persistence.Tables /
// persistence.InstanceTable. The production runtime.RunArgs.Persist
// (a full persistence.Tables) satisfies this interface implicitly.
type matchCounterPersist interface {
	Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error
	Instances() persistence.InstanceTable
}

// incrementMatchCountersAfterMerge persists the per-entry L5 match
// counter for an instance after applyAttributeOverrides has returned
// the matched index list. Extracted so the supervisor → persistence
// seam is unit-testable in isolation (a fake matchCounterPersist can
// capture the call arguments).
//
// Contract:
//   - matched == nil OR len(matched) == 0 → no Transaction call, no
//     persistence touch (steady-state happy path for dispatches with
//     no matcher hits).
//   - matched non-empty → ONE Transaction call wrapping a single
//     IncrementAttributeOverrideMatchCounts(instanceID, matched).
//   - Increment errors are logged via Warn and swallowed — counter
//     loss is observability degradation, not dispatch failure (per
//     spec §"Error handling").
//   - logger may be nil; the helper degrades to silent swallow.
//
// @concept: attribute (L5 matcher overlay)
func incrementMatchCountersAfterMerge(
	ctx context.Context,
	persist matchCounterPersist,
	logger shared.Logger,
	instanceID shared.UUID,
	matched []int,
) {
	if len(matched) == 0 {
		return
	}
	// Open a short dedicated tx for the L5 counter increment.
	// transitionToRunning already committed the dispatch row before
	// we got here, so this tx is separate from any other dispatch-
	// path tx.
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return persist.Instances().IncrementAttributeOverrideMatchCounts(ctx, instanceID, matched, tx)
	})
	if err != nil && logger != nil {
		logger.Warn("instance.attribute_overrides_counter_increment_failed",
			"instance_id", instanceID.String(),
			"matched_indices", matched,
			"error", err.Error())
	}
}
