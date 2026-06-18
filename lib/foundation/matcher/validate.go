// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package matcher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type ValidationRefs struct {
	NodeTypes     map[string]struct{}
	ExecutorNames map[string]struct{}
	UsedExecutors map[string]struct{}
	GraphNames    map[string]struct{}
	LegacyFlat    bool
}

func Validate(m Matcher, refs ValidationRefs, entryIndex int) error {
	prefix := ""
	if entryIndex >= 0 {
		prefix = fmt.Sprintf("[%d]", entryIndex)
	}
	wrap := func(format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		return shared.Wrap(ErrInvalid, "matcher"+prefix+": "+msg, nil)
	}

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

func isPrimitive(v any) bool {
	switch v.(type) {
	case string, bool, float64, int, int64:
		return true
	case json.Number:
		return true
	}
	return false
}
