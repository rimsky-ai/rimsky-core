// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package matcher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// ValidationRefs supplies the reference name-sets and policy flags
// the validator uses. Most fields are optional; when a set is nil
// the corresponding cross-check is skipped.
//
// The by_match wire-shape validator supplies all fields including
// UsedExecutors and LegacyFlat to preserve the existing behavior
// (executor must be referenced by some template node; the "graph:"
// matcher key is rejected when the template has no declared
// sub-graphs). The breakpoint validator supplies NodeTypes,
// ExecutorNames, and GraphNames; leaves UsedExecutors=nil (no such
// constraint for breakpoints) and LegacyFlat=false (breakpoints
// accept "graph:" on any template).
type ValidationRefs struct {
	NodeTypes     map[string]struct{} // when non-nil, node_type must be a member
	ExecutorNames map[string]struct{} // when non-nil, executor must be a member
	UsedExecutors map[string]struct{} // when non-nil, executor must additionally be referenced by some template node (by_match-specific)
	GraphNames    map[string]struct{} // when non-nil, graph must be a member (typically "main" plus declared sub-graphs)
	LegacyFlat    bool                // when true, the "graph:" matcher key is rejected entirely (legacy template with no declared sub-graphs)
}

// Validate enforces the matcher's grammar at registration time.
// Returns nil on success or an error wrapping ErrInvalid.
//
// Validation rules (per spec 2026-05-21-attribute-overrides-matcher-overlay-design
// and 2026-05-24-instance-debugger-design):
//
//   - Unknown matcher keys rejected.
//   - Ordinal-shaped keys (dispatch_index, nth_child, partition_index, seq) rejected.
//   - child_key MUST be a non-empty string (empty string is the
//     non-fan-out sentinel).
//   - node_type, executor, graph values cross-checked against the
//     refs sets when supplied.
//   - attrs values must be primitives.
//
// The entryIndex parameter is the matcher's position in an outer
// list (for by_match's per-entry error messages). Callers without
// a list (e.g., breakpoint creation) pass -1 to suppress the
// "[N]" prefix.
func Validate(m Matcher, refs ValidationRefs, entryIndex int) error {
	prefix := ""
	if entryIndex >= 0 {
		prefix = fmt.Sprintf("[%d]", entryIndex)
	}
	wrap := func(format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		return shared.Wrap(ErrInvalid, "matcher"+prefix+": "+msg, nil)
	}

	// Reject ordinal-shaped keys with a redirect message.
	ordinals := []string{"dispatch_index", "nth_child", "partition_index", "seq"}
	for _, k := range ordinals {
		if _, ok := m[k]; ok {
			return wrap("ordinal key %q rejected — use child_key for per-partition routing or attrs.<path> for attribute-based routing", k)
		}
	}

	for k, v := range m {
		if _, ok := allowedKeys[k]; !ok {
			return wrap("unknown matcher key %q (allowed: node_type, executor, graph, child_key, attrs)", k)
		}
		switch k {
		case "node_type":
			s, ok := v.(string)
			if !ok || s == "" {
				return wrap("matcher.node_type must be a non-empty string")
			}
			if refs.NodeTypes != nil {
				if _, known := refs.NodeTypes[s]; !known {
					return wrap("matcher.node_type: unknown node %q", s)
				}
			}
		case "executor":
			s, ok := v.(string)
			if !ok || s == "" {
				return wrap("matcher.executor must be a non-empty string")
			}
			if refs.ExecutorNames != nil {
				if _, known := refs.ExecutorNames[s]; !known {
					return wrap("matcher.executor: unknown executor name %q", s)
				}
			}
			if refs.UsedExecutors != nil {
				if _, used := refs.UsedExecutors[s]; !used {
					return wrap("matcher.executor: executor not referenced by any template node: %q", s)
				}
			}
		case "graph":
			s, ok := v.(string)
			if !ok || s == "" {
				return wrap("matcher.graph must be a non-empty string (\"main\" or a declared sub-graph name)")
			}
			if refs.LegacyFlat {
				// Legacy flat templates (no declared sub-graphs) accept
				// only "main"; other graph names are not declarable.
				// Preserves existing by_match validator behavior.
				if s != "main" {
					return wrap("matcher.graph: template has no declared sub-graphs; only \"main\" is valid (got %q)", s)
				}
			}
			if refs.GraphNames != nil {
				if _, known := refs.GraphNames[s]; !known {
					return wrap("matcher.graph: unknown graph %q (must be \"main\" or a declared sub-graph name)", s)
				}
			}
		case "child_key":
			s, ok := v.(string)
			if !ok || s == "" {
				return wrap("matcher.child_key must be a non-empty string (empty string is the non-fan-out sentinel, not a matcher target)")
			}
		case "attrs":
			attrs, ok := v.(map[string]any)
			if !ok {
				return wrap("matcher.attrs must be an object")
			}
			for path, want := range attrs {
				if !isPrimitive(want) {
					return wrap("matcher.attrs.%s must be a primitive (string, bool, number); got %T", path, want)
				}
				if strings.TrimSpace(path) == "" {
					return wrap("matcher.attrs key must be a non-empty dotted path")
				}
			}
		}
	}
	return nil
}

// isPrimitive returns true if v is a JSON primitive (string, bool,
// number including json.Number).
func isPrimitive(v any) bool {
	switch v.(type) {
	case string, bool, float64, int, int64:
		return true
	case json.Number:
		return true
	}
	return false
}
