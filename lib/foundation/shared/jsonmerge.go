// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: inertness

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
