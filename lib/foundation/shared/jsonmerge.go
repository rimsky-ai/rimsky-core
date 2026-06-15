// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: inertness alignment — the merge does not inspect, validate, or transform the payload.

package shared

// DeepMergeJSON merges `over` into a copy of `base` and returns the
// result. Behavior:
//
//   - Both args must be JSON-shaped values (the result of json.Unmarshal
//     into `any`): map[string]any, []any, scalars, nil.
//   - When both layers carry an object at the same key, recurse.
//   - When the layers disagree on shape (e.g. base has an object, over
//     has a string), `over` replaces `base` wholesale — last-writer-wins
//     within a layer pair.
//   - Arrays REPLACE, never concatenate. Concatenation would be too cute
//     and ambiguous (do duplicates dedupe? does ordering carry?). If a
//     caller wants concatenation they can express it explicitly in their
//     domain.
//   - Scalars (strings, numbers, bools, nil) replace.
//   - The function never mutates either argument — `base` is cloned as
//     the merge proceeds. Safe to pass references read from persistence.
//   - nil inputs are normalised: a nil `base` is treated as an empty
//     object when `over` is an object; a nil `over` returns a clone of
//     `base`.
func DeepMergeJSON(base, over any) any {
	if over == nil {
		return cloneJSON(base)
	}
	bm, baseIsMap := base.(map[string]any)
	om, overIsMap := over.(map[string]any)
	if !baseIsMap || !overIsMap {
		return cloneJSON(over)
	}
	out := make(map[string]any, len(bm)+len(om))
	for k, v := range bm {
		out[k] = cloneJSON(v)
	}
	for k, v := range om {
		if existing, ok := out[k]; ok {
			out[k] = DeepMergeJSON(existing, v)
			continue
		}
		out[k] = cloneJSON(v)
	}
	return out
}

// cloneJSON deep-copies a JSON-shaped value so callers can freely
// mutate the result of DeepMergeJSON without affecting the inputs.
// Maps and slices are recursively cloned; scalars (strings, numbers,
// bools, nil) are returned as-is (Go semantics make scalar copies free).
func cloneJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = cloneJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = cloneJSON(vv)
		}
		return out
	default:
		return v
	}
}
