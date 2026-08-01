// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: inertness
package matcher

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var ErrInvalid = errors.New("matcher invalid")

type Matcher map[string]any

type Context struct {
	Executor string
	NodeType string
	Graph    string
	ChildKey string
	// @concept: attribute
	AttributeBag map[string]any
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
	malformed := func(key string) bool {
		if logger != nil {
			logger.Warn("matcher.Evaluate: matcher key has the wrong type; skipping entry",
				"entry_index", entryIndex,
				"key", key)
		}
		return false
	}
	if v, ok := m["node_type"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return malformed("node_type")
		}
		if s != ctx.NodeType {
			return false
		}
	}
	if v, ok := m["executor"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return malformed("executor")
		}
		if s != ctx.Executor {
			return false
		}
	}
	if v, ok := m["graph"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return malformed("graph")
		}
		if s != ctx.Graph {
			return false
		}
	}
	if v, ok := m["child_key"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return malformed("child_key")
		}
		if s != ctx.ChildKey {
			return false
		}
	}
	if v, ok := m["attrs"]; ok {
		// @concept: inertness
		attrsMatcher, isMap := v.(map[string]any)
		if !isMap {
			return malformed("attrs")
		}
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
	an, aIsNumber := a.(json.Number)
	bn, bIsNumber := b.(json.Number)
	if aIsNumber && bIsNumber {
		af, aErr := an.Float64()
		bf, bErr := bn.Float64()
		if aErr == nil && bErr == nil {
			return af == bf
		}
		return an.String() == bn.String()
	}
	if aIsNumber {
		if f, err := an.Float64(); err == nil {
			a = f
		}
	}
	if bIsNumber {
		if f, err := bn.Float64(); err == nil {
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
			return floatEqualsInt64(av, int64(bv))
		case int64:
			return floatEqualsInt64(av, bv)
		}
		return false
	case int:
		switch bv := b.(type) {
		case float64:
			return floatEqualsInt64(bv, int64(av))
		case int:
			return av == bv
		case int64:
			return int64(av) == bv
		}
		return false
	case int64:
		switch bv := b.(type) {
		case float64:
			return floatEqualsInt64(bv, av)
		case int:
			return av == int64(bv)
		case int64:
			return av == bv
		}
		return false
	}
	return false
}

func floatEqualsInt64(f float64, i int64) bool {
	if f != math.Trunc(f) || f < math.MinInt64 || f > math.MaxInt64 {
		return false
	}
	return int64(f) == i
}
