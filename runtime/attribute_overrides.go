// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
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
			matcher, _ := entry["matcher"].(map[string]any)
			overlay, _ := entry["overlay"].(map[string]any)
			if !evaluateMatcher(matcher, executor, nodeName, graph, childKey, matcherCtx, logger, i) {
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

// matcherAllowedKeys is the closed set of recognised matcher keys.
// The validator at instance-create rejects unknown keys, so the
// runtime should only ever see this set; the check inside
// evaluateMatcher is defensive against out-of-band persistence
// corruption (matching the same shape of degradation lookupMatchList
// uses for malformed per-entry shapes).
var matcherAllowedKeys = map[string]struct{}{
	"node_type": {},
	"executor":  {},
	"graph":     {},
	"child_key": {},
	"attrs":     {},
}

// evaluateMatcher returns true if matcher's predicate matches the
// dispatch context. AND across all present matcher keys. Missing
// matcher keys are wildcards. Empty matcher ({}) matches every
// dispatch.
//
// The `node_type`, `executor`, `graph`, and `child_key` branches read
// dispatch-identity strings (NOT attribute values), so they are not
// covered by concept:inertness. The narrow inertness-sanctioned read
// site is the `attrs` branch — see the in-function annotation.
//
// `entryIndex` is the matcher's position in the instance's
// `by_match` list; threaded through for the unknown-key warn log so
// operators can find the malformed slot. `logger` may be nil — the
// helper degrades to silent skip in that case.
func evaluateMatcher(
	matcher map[string]any,
	executor, nodeName, graph, childKey string,
	bag map[string]any,
	logger shared.Logger,
	entryIndex int,
) bool {
	// Defensive guard: if the matcher carries any key outside the
	// closed allowed set, treat the entry as malformed and skip it.
	// Without this check a matcher of the form
	// `{"bogus_key": "x"}` (with `len(matcher) > 0` and no recognised
	// keys) would skip every branch's check and return true, firing
	// on every dispatch. The validator rejects unknown keys at
	// instance-create; this runtime guard catches out-of-band
	// persistence corruption (mirrors lookupMatchList's per-entry
	// shape degradation discipline).
	for k := range matcher {
		if _, ok := matcherAllowedKeys[k]; !ok {
			if logger != nil {
				logger.Warn("applyAttributeOverrides: matcher contains unknown key; skipping entry",
					"entry_index", entryIndex,
					"unknown_key", k)
			}
			return false
		}
	}
	if len(matcher) == 0 {
		return true // empty matcher matches every dispatch
	}
	if v, ok := matcher["node_type"]; ok {
		s, _ := v.(string)
		if s != nodeName {
			return false
		}
	}
	if v, ok := matcher["executor"]; ok {
		s, _ := v.(string)
		if s != executor {
			return false
		}
	}
	if v, ok := matcher["graph"]; ok {
		s, _ := v.(string)
		if s != graph {
			return false
		}
	}
	if v, ok := matcher["child_key"]; ok {
		s, _ := v.(string)
		if s != childKey {
			return false
		}
	}
	if v, ok := matcher["attrs"]; ok {
		// Sanctioned attribute-value read site per concept:inertness:
		// rimsky reads the resolved post-L4 attribute bag here for
		// primitive-equality matching only. No traversal beyond the
		// named dotted path; values are never logged, formatted, or
		// included in error messages.
		//
		// @concept: inertness (sanctioned attribute-value read site)
		attrsMatcher, _ := v.(map[string]any)
		for path, want := range attrsMatcher {
			got, found := walkAttrPath(bag, path)
			if !found {
				return false
			}
			if !primitiveEqual(got, want) {
				return false
			}
		}
	}
	return true
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

// walkAttrPath walks a dotted path through bag and returns the leaf
// value plus whether the path resolved. Returns (nil, false) for any
// non-map intermediate (the matcher only addresses primitive leaves
// under composites).
func walkAttrPath(bag map[string]any, path string) (any, bool) {
	cur := any(bag)
	parts := strings.Split(path, ".")
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[p]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// primitiveEqual compares two values for equality, returning false
// when either side is non-primitive. Type-coerces JSON numbers
// (float64 vs int vs json.Number) because matcher values can arrive
// through several decoders:
//   - default json.Unmarshal → float64
//   - json.Decoder with UseNumber() → json.Number
//   - direct Go construction (tests, in-process callers) → int / int64
//
// The validator (control/controlapi/attribute_overrides.go::isPrimitive)
// accepts json.Number on the matcher side; the runtime MUST recognise
// it on either side of the equality, otherwise validator-accepted
// shapes silently fail to match here.
func primitiveEqual(a, b any) bool {
	// Reduce json.Number on either side to its float64 representation
	// for the numeric branches below. Bool / string fall through to
	// the type-specific cases unchanged.
	if n, ok := a.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			a = f
		}
	}
	if n, ok := b.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			b = f
		}
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		switch bv := b.(type) {
		case float64:
			return av == bv
		case int:
			return av == float64(bv)
		case int64:
			return av == float64(bv)
		}
		return false
	case int:
		switch bv := b.(type) {
		case float64:
			return float64(av) == bv
		case int:
			return av == bv
		case int64:
			return int64(av) == bv
		}
		return false
	case int64:
		switch bv := b.(type) {
		case float64:
			return float64(av) == bv
		case int:
			return av == int64(bv)
		case int64:
			return av == bv
		}
		return false
	}
	return false
}
