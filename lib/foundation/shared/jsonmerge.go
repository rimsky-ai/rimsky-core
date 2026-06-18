// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: inertness alignment — the merge does not inspect, validate, or transform the payload.

package shared

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
