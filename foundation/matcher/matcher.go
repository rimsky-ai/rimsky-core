// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package matcher implements the closed five-key dispatch-identity
// predicate shared by concept:attribute (the by_match overlay) and
// concept:breakpoint (the runtime pause-point matcher).
//
// Grammar: equality-only across a fixed key set
// {node_type, executor, graph, child_key, attrs}; AND across present
// keys; missing keys are wildcards; empty matcher fires for every
// dispatch.
//
// The attrs.<path> branch is the inertness-sanctioned attribute-value
// read site (preserved from runtime/attribute_overrides.go's prior
// home); see concept:inertness.
//
// @concept: inertness (sanctioned attribute-value read site lives in Evaluate's attrs branch)
package matcher

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/fallguy/rimsky/foundation/shared"
)

// ErrInvalid is the package-local sentinel returned by Validate for any
// grammar or cross-check failure. Callers use errors.Is(err, matcher.ErrInvalid)
// to convert to HTTP 400 / MCP InvalidParams at their boundary. Pattern
// mirrors the existing control/controlapi/attribute_overrides.go::errAttributeOverridesInvalid.
var ErrInvalid = errors.New("matcher invalid")

// Matcher is the closed-key-set predicate. The wire form is JSON;
// the Go form is a generic map (the runtime never inspects shape
// beyond the keys this package owns).
type Matcher map[string]any

// Context is the dispatch context the matcher evaluates against.
type Context struct {
	Executor     string
	NodeType     string
	Graph        string
	ChildKey     string
	AttributeBag map[string]any // post-L5 merged attributes per concept:attribute
}

// allowedKeys is the closed set of recognised matcher keys.
// The Validate function rejects unknown keys at registration; Evaluate
// defensively skips entries with unknown keys (matching the existing
// runtime discipline against out-of-band persistence corruption).
var allowedKeys = map[string]struct{}{
	"node_type": {},
	"executor":  {},
	"graph":     {},
	"child_key": {},
	"attrs":     {},
}

// Evaluate returns true if matcher fires on ctx. AND-joined across
// present keys; missing keys are wildcards; empty matcher matches
// every dispatch. If the matcher carries any key outside the closed
// allowed set, returns false and emits a Warn (out-of-band persistence
// corruption — the validator rejects this at registration).
//
// The attrs.<path> branch is the concept:inertness sanctioned
// attribute-value read site.
func Evaluate(m Matcher, ctx Context, logger shared.Logger, entryIndex int) bool {
	// Defensive guard against unknown keys (out-of-band corruption).
	for k := range m {
		if _, ok := allowedKeys[k]; !ok {
			if logger != nil {
				logger.Warn("matcher.Evaluate: matcher contains unknown key; skipping entry",
					"entry_index", entryIndex,
					"unknown_key", k)
			}
			return false
		}
	}
	if len(m) == 0 {
		return true
	}
	if v, ok := m["node_type"]; ok {
		s, _ := v.(string)
		if s != ctx.NodeType {
			return false
		}
	}
	if v, ok := m["executor"]; ok {
		s, _ := v.(string)
		if s != ctx.Executor {
			return false
		}
	}
	if v, ok := m["graph"]; ok {
		s, _ := v.(string)
		if s != ctx.Graph {
			return false
		}
	}
	if v, ok := m["child_key"]; ok {
		s, _ := v.(string)
		if s != ctx.ChildKey {
			return false
		}
	}
	if v, ok := m["attrs"]; ok {
		// @concept: inertness (sanctioned attribute-value read site)
		attrsMatcher, _ := v.(map[string]any)
		for path, want := range attrsMatcher {
			got, found := walkAttrPath(ctx.AttributeBag, path)
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

// walkAttrPath walks a dotted path through bag and returns the leaf
// value plus whether the path resolved. Returns (nil, false) for any
// non-map intermediate.
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

// primitiveEqual compares two values for equality. Type-coerces
// numeric values across float64 / int / int64 / json.Number per the
// existing by_match validator + runtime convention. Returns false
// when either side is non-primitive.
func primitiveEqual(a, b any) bool {
	// Reduce json.Number on either side to float64 for the numeric
	// branches.
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
