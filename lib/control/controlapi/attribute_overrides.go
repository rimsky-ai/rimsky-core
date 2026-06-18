// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

var errAttributeOverridesInvalid = errors.New("attribute_overrides invalid")

func validateAttributeOverrides(
	overrides map[string]any,
	templateNodes []nodepkg.TemplateNodeDef,
	templateGraphs []spec.GraphSpec,
	executors map[string]ExecutorEntry,
) error {
	if len(overrides) == 0 {
		return nil
	}
	for k := range overrides {
		if k != "by_executor" && k != "by_node" && k != "by_match" {
			return wrapInvalidf("attribute_overrides: unknown top-level key (allowed: by_executor, by_node, by_match); got %q", k)
		}
	}

	if raw, ok := overrides["by_executor"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return wrapInvalid("attribute_overrides.by_executor must be an object")
		}
		usedExecutors := make(map[string]struct{}, len(templateNodes))
		for _, n := range templateNodes {
			if n.Executor != "" {
				usedExecutors[n.Executor] = struct{}{}
			}
		}
		for name, val := range m {
			if _, isObj := val.(map[string]any); !isObj {
				return wrapInvalidf("attribute_overrides.by_executor entry must be an object: %q", name)
			}
			if _, declared := executors[name]; !declared {
				return wrapInvalidf("attribute_overrides.by_executor: unknown executor name %q", name)
			}
			if _, used := usedExecutors[name]; !used {
				return wrapInvalidf("attribute_overrides.by_executor: executor not referenced by any template node: %q", name)
			}
		}
	}

	if raw, ok := overrides["by_node"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return wrapInvalid("attribute_overrides.by_node must be an object")
		}
		nodeNames := make(map[string]struct{}, len(templateNodes))
		for _, n := range templateNodes {
			nodeNames[n.Type] = struct{}{}
		}
		for name, val := range m {
			if _, isObj := val.(map[string]any); !isObj {
				return wrapInvalidf("attribute_overrides.by_node entry must be an object: %q", name)
			}
			if _, ok := nodeNames[name]; !ok {
				return wrapInvalidf("attribute_overrides.by_node: unknown node name %q", name)
			}
		}
	}

	if raw, ok := overrides["by_match"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return wrapInvalid("attribute_overrides.by_match must be an array")
		}
		if err := validateMatchEntries(list, templateNodes, templateGraphs, executors); err != nil {
			return err
		}
	}
	return nil
}

func validateMatchEntries(
	list []any,
	templateNodes []nodepkg.TemplateNodeDef,
	templateGraphs []spec.GraphSpec,
	executors map[string]ExecutorEntry,
) error {
	nodeNames := make(map[string]struct{}, len(templateNodes))
	usedExecutors := make(map[string]struct{}, len(templateNodes))
	for _, n := range templateNodes {
		nodeNames[n.Type] = struct{}{}
		if n.Executor != "" {
			usedExecutors[n.Executor] = struct{}{}
		}
	}
	graphNames := make(map[string]struct{}, len(templateGraphs)+1)
	graphNames[spec.MainGraphName] = struct{}{}
	for _, g := range templateGraphs {
		graphNames[g.Name] = struct{}{}
	}
	legacyFlat := len(templateGraphs) == 0

	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return wrapInvalidf("attribute_overrides.by_match[%d] must be an object", i)
		}
		for k := range entry {
			if k != "matcher" && k != "overlay" {
				return wrapInvalidf("attribute_overrides.by_match[%d]: unknown entry key (allowed: matcher, overlay); got %q", i, k)
			}
		}
		var matcher map[string]any
		if rawM, present := entry["matcher"]; present {
			if rawM == nil {
				matcher = map[string]any{}
			} else {
				m, isObj := rawM.(map[string]any)
				if !isObj {
					return wrapInvalidf("attribute_overrides.by_match[%d].matcher must be an object (or null / absent for a wildcard match)", i)
				}
				matcher = m
			}
		} else {
			matcher = map[string]any{}
		}
		if _, isObj := entry["overlay"].(map[string]any); !isObj {
			return wrapInvalidf("attribute_overrides.by_match[%d].overlay is required and must be an object", i)
		}
		if err := validateMatcherKeys(i, matcher, nodeNames, usedExecutors, executors, graphNames, legacyFlat); err != nil {
			return err
		}
	}
	return nil
}

func validateMatcherKeys(
	entryIdx int,
	matcherMap map[string]any,
	nodeNames, usedExecutors map[string]struct{},
	executors map[string]ExecutorEntry,
	graphNames map[string]struct{},
	legacyFlat bool,
) error {
	execNames := make(map[string]struct{}, len(executors))
	for name := range executors {
		execNames[name] = struct{}{}
	}
	err := matcher.Validate(matcher.Matcher(matcherMap), matcher.ValidationRefs{
		NodeTypes:     nodeNames,
		ExecutorNames: execNames,
		UsedExecutors: usedExecutors,
		GraphNames:    graphNames,
		LegacyFlat:    legacyFlat,
	}, entryIdx)
	if err != nil {
		return shared.Wrap(errAttributeOverridesInvalid, err.Error(), nil)
	}
	return nil
}

func wrapInvalid(msg string) error {
	return fmt.Errorf("%s: %w", msg, errAttributeOverridesInvalid)
}

func wrapInvalidf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, errAttributeOverridesInvalid)...)
}

func overridePresentKeys(overrides map[string]any) (byExecutor, byNode []string, byMatchCount int) {
	if raw, ok := overrides["by_executor"]; ok {
		if m, ok := raw.(map[string]any); ok {
			for k := range m {
				byExecutor = append(byExecutor, k)
			}
			sort.Strings(byExecutor)
		}
	}
	if raw, ok := overrides["by_node"]; ok {
		if m, ok := raw.(map[string]any); ok {
			for k := range m {
				byNode = append(byNode, k)
			}
			sort.Strings(byNode)
		}
	}
	if raw, ok := overrides["by_match"]; ok {
		if list, ok := raw.([]any); ok {
			byMatchCount = len(list)
		}
	}
	return byExecutor, byNode, byMatchCount
}

func overridesEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
