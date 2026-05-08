// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	nodepkg "github.com/fallguy/rimsky/modeling/node"
)

// errUserdataOverridesInvalid is the sentinel returned by
// validateUserdataOverrides when the override blob is shape-malformed
// or names an unknown executor / node. The handler maps it to HTTP
// 400 (bad request) — distinct from ErrTemplateValidation, which is
// 409 in the instance-create path because that error reflects template
// state-machine conflicts, not malformed request data.
var errUserdataOverridesInvalid = errors.New("userdata_overrides invalid")

// validateUserdataOverrides enforces the wire shape of
// rimsky_instances.userdata_overrides:
//
//	{
//	  "by_executor": {"<executor-name>": { ...userdata-fragment... }},
//	  "by_node":     {"<node-name>":     { ...userdata-fragment... }}
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
// The fragment values themselves are NOT inspected — they're userdata
// per @blessed-invariant 11. This validator only inspects keys and
// container shapes (objects vs everything else); contents are opaque.
//
// Errors wrap errUserdataOverridesInvalid; callers map to HTTP 400.
func validateUserdataOverrides(
	overrides map[string]any,
	templateNodes []nodepkg.TemplateNodeDef,
	executors map[string]ExecutorEntry,
) error {
	if len(overrides) == 0 {
		return nil
	}
	for k := range overrides {
		if k != "by_executor" && k != "by_node" {
			return wrapInvalidf("userdata_overrides: unknown top-level key (allowed: by_executor, by_node); got %q", k)
		}
	}

	if raw, ok := overrides["by_executor"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return wrapInvalid("userdata_overrides.by_executor must be an object")
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
				return wrapInvalidf("userdata_overrides.by_executor entry must be an object: %q", name)
			}
			if _, declared := executors[name]; !declared {
				return wrapInvalidf("userdata_overrides.by_executor: unknown executor name %q", name)
			}
			if _, used := usedExecutors[name]; !used {
				return wrapInvalidf("userdata_overrides.by_executor: executor not referenced by any template node: %q", name)
			}
		}
	}

	if raw, ok := overrides["by_node"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return wrapInvalid("userdata_overrides.by_node must be an object")
		}
		nodeNames := make(map[string]struct{}, len(templateNodes))
		for _, n := range templateNodes {
			nodeNames[n.Type] = struct{}{}
		}
		for name, val := range m {
			if _, isObj := val.(map[string]any); !isObj {
				return wrapInvalidf("userdata_overrides.by_node entry must be an object: %q", name)
			}
			if _, ok := nodeNames[name]; !ok {
				return wrapInvalidf("userdata_overrides.by_node: unknown node name %q", name)
			}
		}
	}
	return nil
}

// wrapInvalid annotates msg with the errUserdataOverridesInvalid
// sentinel via fmt's %w verb; callers use errors.Is to detect.
func wrapInvalid(msg string) error {
	return fmt.Errorf("%s: %w", msg, errUserdataOverridesInvalid)
}

// wrapInvalidf is the formatting variant. Use this when the message
// includes user-supplied JSON keys: callers should pass the key with
// the %q verb so the rendered key is Go-quoted (newlines, control
// characters, and other shenanigans escape into the literal). Eliminates
// any HTTP-response-injection surface from operator-supplied bytes
// reaching the 400 body via badRequest(w, err.Error()).
func wrapInvalidf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, errUserdataOverridesInvalid)...)
}

// overridePresentKeys extracts the executor + node names listed under
// the by_executor / by_node sub-maps. The fragment values are NOT
// returned — only the names, suitable for structured-log audit emission
// without leaking opaque userdata payload to logs.
//
// Returns (byExecutor, byNode), each sorted, each may be nil when the
// corresponding sub-map is absent or empty.
//
// Precondition: callers MUST have validated the override blob via
// validateUserdataOverrides first. This helper assumes well-formed
// shape (`by_executor` / `by_node` are `map[string]any` if present)
// and silently returns empty slices for malformed input — the audit
// log would otherwise mask shape errors. Validator-first wiring is the
// only way this helper sees malformed input on the hot path.
func overridePresentKeys(overrides map[string]any) (byExecutor, byNode []string) {
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
	return byExecutor, byNode
}

// overridesEqual returns true when two override blobs are structurally
// identical. Used to suppress the
// `instance.userdata_overrides_replaced_by_idempotent_match` WARN when
// an operator's reconcile loop issues idempotent retries with the same
// body — without this comparison, every reconcile would emit a noisy
// "discarded" warning even though the values are identical.
//
// reflect.DeepEqual handles the deeply-nested map[string]any shape
// validateUserdataOverrides admits (objects of objects of any). Both
// sides are post-validation / post-persistence shapes: nil and empty
// `map[string]any{}` should compare equal because the column persists
// `{}` for absent overrides and the request body may be either.
func overridesEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
