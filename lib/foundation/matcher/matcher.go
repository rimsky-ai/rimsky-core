// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: inertness (sanctioned attribute-value read site lives in Evaluate's attrs branch)
package matcher

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrInvalid = errors.New("matcher invalid")

type Matcher map[string]any

type Context struct {
	Executor     string
	NodeType     string
	Graph        string
	ChildKey     string
	AttributeBag map[string]any // @concept: attribute — post-L5 merged attribute bag
}

var allowedKeys = map[string]struct{}{
	"node_type": {},
	"executor":  {},
	"graph":     {},
	"child_key": {},
	"attrs":     {},
}

func Evaluate(m Matcher, ctx Context, logger shared.Logger, entryIndex int) bool {
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

func primitiveEqual(a, b any) bool {
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
