// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/fallguyconsulting/rimsky/foundation/matcher"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	nodepkg "github.com/fallguyconsulting/rimsky/graph/node"
)

// errAttributeOverridesInvalid is the sentinel returned by
// validateAttributeOverrides when the override blob is shape-malformed
// or names an unknown executor / node. The handler maps it to HTTP
// 400 (bad request) — distinct from ErrTemplateValidation, which is
// 409 in the instance-create path because that error reflects template
// state-machine conflicts, not malformed request data.
var errAttributeOverridesInvalid = errors.New("attribute_overrides invalid")

// validateAttributeOverrides enforces the wire shape of
// rimsky_instances.attribute_overrides:
//
//	{
//	  "by_executor": {"<executor-name>": { ...attribute-fragment... }},
//	  "by_node":     {"<node-name>":     { ...attribute-fragment... }}
//	}
//
// Both top-level keys are optional; any other top-level key is rejected
// (forwarding it would be a silent no-op on dispatch — better to fail
// loud at create-time so typos surface immediately).
//
// `by_executor` keys must be names declared in the operator's executors
// block (rimsky.yml, surfaced through AppDeps.Executors) AND referenced
// by at least one node in the locked template. A typo that names a real
// executor the template doesn't dispatch to would be a silent no-op at
// dispatch — which the validator's whole purpose is to prevent. `by_node`
// keys must be Type values present in the locked template's nodes.
//
// The fragment values themselves are NOT inspected — they're attribute values
// under concept:inertness (structural-inertness for attribute values). This validator only inspects keys and
// container shapes (objects vs everything else); contents are opaque.
//
// Errors wrap errAttributeOverridesInvalid; callers map to HTTP 400.
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
		// Build the set of executor names this template actually
		// dispatches to. An override targeting an executor declared in
		// rimsky.yml but not used by any template node would be a silent
		// no-op at dispatch — reject it loudly.
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

// validateMatchEntries validates each by_match entry's shape and
// matcher cross-checks against the locked template + declared
// executors. Returns wrapped errAttributeOverridesInvalid on any
// failure.
func validateMatchEntries(
	list []any,
	templateNodes []nodepkg.TemplateNodeDef,
	templateGraphs []spec.GraphSpec,
	executors map[string]ExecutorEntry,
) error {
	// Build name sets once.
	nodeNames := make(map[string]struct{}, len(templateNodes))
	usedExecutors := make(map[string]struct{}, len(templateNodes))
	for _, n := range templateNodes {
		nodeNames[n.Type] = struct{}{}
		if n.Executor != "" {
			usedExecutors[n.Executor] = struct{}{}
		}
	}
	graphNames := make(map[string]struct{}, len(templateGraphs)+1)
	graphNames[spec.MainGraphName] = struct{}{} // "main" always valid
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
		// matcher: accept missing OR explicit null as implicit {} (both
		// mean "match every dispatch" — runtime evaluateMatcher returns
		// true when len(matcher) == 0, and a JSON producer may emit a
		// nil matcher as either an absent key or `"matcher": null`).
		// Non-object values OTHER than null are invalid (a JSON array
		// or scalar matcher would be a typo, not a wildcard).
		var matcher map[string]any
		if rawM, present := entry["matcher"]; present {
			if rawM == nil {
				matcher = map[string]any{} // explicit null → wildcard
			} else {
				m, isObj := rawM.(map[string]any)
				if !isObj {
					return wrapInvalidf("attribute_overrides.by_match[%d].matcher must be an object (or null / absent for a wildcard match)", i)
				}
				matcher = m
			}
		} else {
			matcher = map[string]any{} // absent → wildcard
		}
		// overlay must be present AND be an object. A single
		// type-assertion catches both the missing-key case (zero-value
		// nil fails the assertion) and the wrong-type case.
		if _, isObj := entry["overlay"].(map[string]any); !isObj {
			return wrapInvalidf("attribute_overrides.by_match[%d].overlay is required and must be an object", i)
		}
		if err := validateMatcherKeys(i, matcher, nodeNames, usedExecutors, executors, graphNames, legacyFlat); err != nil {
			return err
		}
	}
	return nil
}

// validateMatcherKeys enforces the matcher grammar's key set,
// per-key cross-checks, and ordinal-shaped-key rejection.
//
// Delegates to foundation/matcher.Validate. The control-api layer
// owns the projection from its locally-typed ExecutorEntry map to the
// matcher package's generic set, and re-wraps the matcher's
// ErrInvalid sentinel into errAttributeOverridesInvalid so existing
// HTTP-400 translation and error-assertion tests continue to work
// unchanged.
func validateMatcherKeys(
	entryIdx int,
	matcherMap map[string]any,
	nodeNames, usedExecutors map[string]struct{},
	executors map[string]ExecutorEntry,
	graphNames map[string]struct{},
	legacyFlat bool,
) error {
	// Project the locally-typed ExecutorEntry map down to the generic
	// name set the matcher package consumes. The matcher package can't
	// import controlapi.ExecutorEntry (back-cycle).
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
		// Re-wrap to preserve the existing errAttributeOverridesInvalid
		// sentinel so by_match's existing test assertions and
		// HTTP-status translation continue to work unchanged. The
		// breakpoint code path (Pass 4) calls matcher.Validate directly
		// and translates matcher.ErrInvalid at its own boundary.
		return shared.Wrap(errAttributeOverridesInvalid, err.Error(), nil)
	}
	return nil
}

// wrapInvalid annotates msg with the errAttributeOverridesInvalid
// sentinel via fmt's %w verb; callers use errors.Is to detect.
func wrapInvalid(msg string) error {
	return fmt.Errorf("%s: %w", msg, errAttributeOverridesInvalid)
}

// wrapInvalidf is the formatting variant. Use this when the message
// includes user-supplied JSON keys: callers should pass the key with
// the %q verb so the rendered key is Go-quoted (newlines, control
// characters, and other shenanigans escape into the literal). Eliminates
// any HTTP-response-injection surface from operator-supplied bytes
// reaching the 400 body via badRequest(w, err.Error()).
func wrapInvalidf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, errAttributeOverridesInvalid)...)
}

// overridePresentKeys extracts the executor + node names listed under
// the by_executor / by_node sub-maps plus the length of the by_match
// list. The fragment values are NOT returned — only the names and
// the by_match length, suitable for structured-log audit emission
// without leaking opaque attribute fragments to logs.
//
// Returns (byExecutor, byNode, byMatchCount). The two name slices are
// sorted and may be nil when the corresponding sub-map is absent or
// empty; byMatchCount is the length of the by_match array (0 when
// absent or malformed).
//
// Precondition: callers MUST have validated the override blob via
// validateAttributeOverrides first. This helper assumes well-formed
// shape and silently returns empty slices for malformed input — the
// audit log would otherwise mask shape errors. Validator-first
// wiring is the only way this helper sees malformed input on the hot
// path.
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

// overridesEqual returns true when two override blobs are structurally
// identical. Used to suppress the
// `instance.attribute_overrides_replaced_by_idempotent_match` WARN when
// an operator's reconcile loop issues idempotent retries with the same
// body — without this comparison, every reconcile would emit a noisy
// "discarded" warning even though the values are identical.
//
// reflect.DeepEqual handles the deeply-nested map[string]any shape
// validateAttributeOverrides admits (objects of objects of any). Both
// sides are post-validation / post-persistence shapes: nil and empty
// `map[string]any{}` should compare equal because the column persists
// `{}` for absent overrides and the request body may be either.
func overridesEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
