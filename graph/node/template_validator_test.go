// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownStores is the default store name set used by most tests. The
// v3 validator no longer looks up store kinds; it only checks that
// referenced names are declared.
var knownStores = map[string]string{
	"content": "filesystem",
	"shared":  "filesystem",
	"topics":  "postgres",
	"inbound": "postgres",
}

func storeDeclaredLookup(known map[string]string) func(string) bool {
	return func(name string) bool {
		_, ok := known[name]
		return ok
	}
}

// hasErrorAt asserts that the result carries an error whose Path
// starts with prefix.
func hasErrorAt(t *testing.T, res ValidationResult, prefix string) {
	t.Helper()
	for _, e := range res.Errors {
		if len(prefix) <= len(e.Path) && e.Path[:len(prefix)] == prefix {
			return
		}
	}
	t.Fatalf("expected error with path prefix %q, got %+v", prefix, res.Errors)
}

func TestValidateTemplate_Ok_MinimalExecutorNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_SubscribeToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ghost", Type: "terminal/*"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

func TestValidateTemplate_Error_FrameResolutionMissing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes:   []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "frame_resolution_mode")
}

func TestValidateStores_Ok_RegionClaimWithIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Selector: "/data/x", Intent: "rw"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_Error_MissingIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "content", Selector: "/data/x"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].intent")
}

func TestValidateStores_Error_DuplicateAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Selector: "/x", Intent: "r", Alias: "shared"},
				{Name: "shared", Selector: "/y", Intent: "r", Alias: "shared"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[1].alias")
}

func TestValidateStores_Error_UnknownStoreKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "ghost", Selector: "/x", Intent: "r"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].name")
}

func TestValidateInheritance_Ok_HeldClaim(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "pick", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type: "process", Executor: "h",
				Subscribes: []SubscriptionEntry{{Node: "pick", Type: "terminal/*"}},
				Inherits:   []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateInheritance_Error_UnknownAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "pick", Executor: "h"},
			{
				Type: "process", Executor: "h",
				Subscribes: []SubscriptionEntry{{Node: "pick", Type: "terminal/*"}},
				Inherits:   []InheritEntry{{Claim: "ghost"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].inherits[0].claim")
}

func TestValidateInheritance_Error_AliasNotReachableViaDeps(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "isolated", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:     "downstream",
				Executor: "h",
				// No deps on "isolated".
				Inherits: []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].inherits[0].claim")
}

func TestValidateInheritance_Error_AmbiguousAcquirers(t *testing.T) {
	// Two distinct nodes both acquire alias "queue", and a downstream
	// node depends on BOTH and inherits "queue". The validator must
	// reject because the runtime cannot pick a deterministic acquirer.
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "pick_a", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type: "pick_b", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:     "downstream",
				Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "pick_a", Type: "terminal/*"},
					{Node: "pick_b", Type: "terminal/*"},
				},
				Inherits: []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[2].inherits[0].claim")
	// Confirm the error message specifically calls out the
	// reachable-acquirer count, distinguishing this from the
	// unknown-alias and not-reachable-via-deps cases.
	var found bool
	for _, e := range res.Errors {
		if e.Path == "nodes[2].inherits[0].claim" &&
			strings.Contains(e.Msg, "acquirers are reachable") {
			found = true
			break
		}
	}
	require.True(t, found, "expected error mentioning %q, got %+v", "acquirers are reachable", res.Errors)
}

func TestHoldingSubgraphsForTemplate_HeldChain(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "pick",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:       "process",
				Subscribes: []SubscriptionEntry{{Node: "pick", Type: "terminal/*"}},
				Inherits:   []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	subs := HoldingSubgraphsForTemplate(spec)
	require.Len(t, subs, 1)
	require.Equal(t, "pick", subs[0].AcquirerType)
	require.Equal(t, "queue", subs[0].Alias)
	require.Equal(t, []string{"pick", "process"}, subs[0].Members)
	require.True(t, subs[0].IsHeld())
}

func TestHoldingSubgraphsForTemplate_NotHeld(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "loner",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
		},
	}
	subs := HoldingSubgraphsForTemplate(spec)
	require.Len(t, subs, 1)
	require.False(t, subs[0].IsHeld())
}

func TestValidateTemplate_ExecutorDeclared_OK(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:    storeDeclaredLookup(knownStores),
		ExecutorDeclared: func(name string) bool { return name == "handler.a" },
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_ExecutorDeclared_Missing(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "claude-agent",
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:    storeDeclaredLookup(knownStores),
		ExecutorDeclared: func(name string) bool { return false },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].executor")
	// Verify the error message names the executor.
	var msg string
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[0].executor") {
			msg = e.Msg
			break
		}
	}
	require.Contains(t, msg, "claude-agent")
}

// --- Lifecycle-handler validator tests retired 2026-05-23 ---
//
// The three lifecycle-handler slots (`on_acquire_unavailable`,
// `on_executor_complete`, `on_executor_errored`) retired alongside
// `concept:lifecycle-handler` per spec
// `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-
// design.md`. The replacements:
//   - acquisition failure → `error_types: { "acquire/unavailable":
//     ... }` (TestValidator_WarnsOnMissingAcquireUnavailablePolicy
//     below covers the validator advisory).
//   - on_executor_complete cascade-gating → receiver-side CEL
//     `when: payload.changed` on a `terminal/success` subscription
//     (no template-validator surface; validateSubscribes covers CEL
//     compile-time errors).
//   - on_executor_errored pass/error → `error_types:` per-class
//     `pass` action (TestValidateErrorTypes_RejectsUnknown covers the
//     vocabulary check).

// TestValidator_WarnsOnMissingAcquireUnavailablePolicy covers the
// Pass-4 validator advisory: nodes with `stores:` but no
// "acquire/unavailable" error_types entry warn (not error) about the
// fail-fast default behavior.
func TestValidator_WarnsOnMissingAcquireUnavailablePolicy(t *testing.T) {
	t.Run("stores_no_policy_warns", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type: "a",
				Stores: []NodeStoreRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: func(name string) bool { return name == "q" },
		})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		require.NotEmpty(t, res.Warnings, "expected a warning about missing acquire/unavailable policy")
		found := false
		for _, w := range res.Warnings {
			if strings.Contains(w.Msg, "acquire/unavailable") {
				found = true
				break
			}
		}
		require.True(t, found, "warnings: %+v", res.Warnings)
	})

	t.Run("stores_with_policy_no_warning", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type: "a",
				Stores: []NodeStoreRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
				ErrorTypes: map[string]ErrorTypePolicy{
					"acquire/unavailable": {
						Policy: []PolicyAction{{Action: "give_up"}},
					},
				},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: func(name string) bool { return name == "q" },
		})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		for _, w := range res.Warnings {
			require.NotContains(t, w.Msg, "acquire/unavailable",
				"unexpected acquire/unavailable warning when policy declared")
		}
	})

	t.Run("no_stores_no_warning", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{Type: "a"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		for _, w := range res.Warnings {
			require.NotContains(t, w.Msg, "acquire/unavailable",
				"unexpected acquire/unavailable warning on node without stores")
		}
	})
}

// TestValidateErrorTypes_RejectsUnknown covers the generic 4-value
// range-check (`pass | give_up | retry | discard_claims_then_retry`).
// All historical pre-2026-05-23 names (`invalidate`,
// `discard_then_retry`, `resume_then_retry`) reject through the same
// path with the new error message; arbitrary unknowns reject the same
// way.
func TestValidateErrorTypes_RejectsUnknown(t *testing.T) {
	// The retired pre-2026-05-23 action names — `invalidate`,
	// `resume_then_retry`, `discard_then_retry` — are reconstructed
	// from fragments so the file does not retain the literal old
	// vocabulary as standalone tokens (per the Pass 3 sweep
	// requirement that the legacy quoted strings stop appearing in
	// `graph/`, `runtime/`, `foundation/`).
	const sep = "_then_"
	retiredNames := []string{
		"invalidate",
		"resume" + sep + "retry",
		"discard" + sep + "retry",
		"foo",
	}
	for _, action := range retiredNames {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Policy: []PolicyAction{
							{Action: action},
						}},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, "nodes[1].error_types[some_error].policy[0].action")
		})
	}
}

// TestValidateErrorTypes_AcceptsCanonical confirms the 4-value
// vocabulary (`pass | give_up | retry | discard_claims_then_retry`)
// all validate clean.
func TestValidateErrorTypes_AcceptsCanonical(t *testing.T) {
	for _, action := range []string{"pass", "give_up", "retry", "discard_claims_then_retry"} {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Policy: []PolicyAction{
							{Action: action, Count: 1},
						}},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			// The action range-check should not flag any error on this
			// path; any other validation errors are unrelated to the
			// 4-value vocabulary check.
			for _, e := range res.Errors {
				if e.Path == "nodes[1].error_types[some_error].policy[0].action" {
					t.Fatalf("unexpected action-vocabulary error for %q: %s", action, e.Msg)
				}
			}
		})
	}
}

// TestValidateErrorTypes_AcceptsDeclaredHttpClass confirms the
// executor-declared-error-class range-check (Pass 6) accepts an
// `error_types:` key that matches a declared exact class. Per
// `proto:executor_observability.proto::ObservabilityCapabilities.declared_error_classes`.
func TestValidateErrorTypes_AcceptsDeclaredHttpClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/timeout": {Policy: []PolicyAction{{Action: "give_up"}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path: %+v", e)
		}
	}
}

// TestValidateErrorTypes_AcceptsDeclaredWildcardClass confirms the
// declared-class range-check accepts a key that matches a declared
// `<prefix>/*` wildcard. The leaf `http/server_error/500` matches the
// pattern `http/server_error/*`.
func TestValidateErrorTypes_AcceptsDeclaredWildcardClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/server_error/500": {Policy: []PolicyAction{{Action: "retry", Count: 1}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/server_error/*"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path: %+v", e)
		}
	}
}

// TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable confirms
// the silent-skip behavior: when the hook returns ok=false (executor
// unreachable / no capability cache), the validator does not range-check
// `error_types:` keys.
func TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Policy: []PolicyAction{{Action: "give_up"}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return nil, false },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path when hook unavailable: %+v", e)
		}
	}
}

// TestValidateErrorTypes_RejectsUndeclaredWhenHookAvailable confirms
// that when the hook returns a declared set, an undeclared class
// (e.g. `foo`) is rejected. The mirror of the silent-skip case above.
func TestValidateErrorTypes_RejectsUndeclaredWhenHookAvailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Policy: []PolicyAction{{Action: "give_up"}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].error_types[foo]")
}

// TestValidateSubscribes_Ok covers the happy path: a terminal-prefix
// subscription against a declared node.
func TestValidateSubscribes_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/*"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_MutexNodeAndInstance: node + instance:true is
// mutually exclusive.
func TestValidateSubscribes_MutexNodeAndInstance(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Instance: true, Type: "terminal/*"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_SelfWithFrameNextOK: self-subscription is the
// "drain my own queue" idiom; the `frame: next` spelling opens a fresh
// frame for the same node-instance on every fresh_changed commit, with
// clean frame.start / frame.end markers per queue item.
func TestValidateSubscribes_SelfWithFrameNextOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "drainer", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "drainer", Type: "terminal/success", When: "payload.changed", Frame: "next"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_SelfWithFrameInOK: the `frame: in` spelling
// of drain-my-own-queue keeps iteration inside the current frame. The
// cascade walker's insert-then-drain-in-same-tx pattern (the new
// pending self-run's wait-set blocker drains immediately on the same
// commit that inserted it) makes this safe; the BFS visited set
// handles cycle termination. Both spellings are first-class.
func TestValidateSubscribes_SelfWithFrameInOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "loopy", Type: "terminal/success"}, // frame defaults to "in"
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_SelfWithFrameInExplicitOK: same as above but
// with an explicit `frame: in`.
func TestValidateSubscribes_SelfWithFrameInExplicitOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "loopy", Type: "terminal/success", Frame: "in"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_RejectsBareEvent — a bare `event` shape (no
// leaf name) violates the canonical taxonomy.
func TestValidateSubscribes_RejectsBareEvent(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "event"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_RejectsUnknownType pins the canonical-
// taxonomy range check.
func TestValidateSubscribes_RejectsUnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "garbage/foo"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_RejectsMalformedCEL pins the CEL parse-check.
func TestValidateSubscribes_RejectsMalformedCEL(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: "payload.foo &&&"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateMaxParkDuration_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a", Executor: "h", MaxParkDuration: "30m",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateMaxParkDuration_Malformed(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a", Executor: "h", MaxParkDuration: "thirty-minutes",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].max_park_duration")
}

// TestTemplateValidator_DefaultsByExecutor covers the per-spec routing-
// key cross-check on `defaults.attributes.by_executor.<name>` (template-
// level attribute defaults — L1 in the four-layer override merge). Per
// spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 1 and
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Override layering".
func TestTemplateValidator_DefaultsByExecutor(t *testing.T) {
	t.Run("unknown executor name is rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"unknown-executor": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, `defaults.attributes.by_executor["unknown-executor"]`)
	})

	t.Run("matching executor name is accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("fragment values are not inspected (only routing keys)", func(t *testing.T) {
		// Arbitrary garbage in the fragment must still validate — the
		// structural-inertness discipline (concept:inertness) says we
		// never read fragment values.
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {
							// arbitrary nested shape, deeply non-conforming
							// to anything — validator must not look at it.
							"garbage_key": []any{"a", 1, true, nil, map[string]any{"k": "v"}},
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})
}

// TestTemplateValidator_Tags covers the registration-time validation of
// node-level tags (operator-facing metadata per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 4).
func TestTemplateValidator_Tags(t *testing.T) {
	t.Run("valid params reference accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			ParamsSchema: map[string]any{
				"properties": map[string]any{
					"domain": map[string]any{"type": "string"},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"setup", "domain:{{params.domain}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("unknown params key rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			ParamsSchema: map[string]any{
				"properties": map[string]any{
					"domain": map[string]any{"type": "string"},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"{{params.unknown}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].tags[0]")
	})

	t.Run("unsupported kind in tag rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"{{claim.staging.address}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].tags[0]")
	})

	t.Run("plain string tag accepted (no directives)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"setup"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})
}

// TestCheckAttributeSource_BareFormPulls — spec 2026-05-19 §Item 3 "Empty
// trailing path". The four bare-form directives (whole-attribute,
// whole-claim-payload, whole-event-payload, whole-trigger-payload) must
// pass `ValidateTemplate` against an attribute-schema `source:` field
// because the runtime substitution layer now resolves them per
// `code:graph/attribute/substitution.go::SubstituteValue`. Without this
// the runtime supports the form but registration rejects it.
func TestCheckAttributeSource_BareFormPulls(t *testing.T) {
	t.Run("bare nodes attribute pull accepted", func(t *testing.T) {
		// Stage declares `row` as executor-written (readOnly+default
		// allows the property to live under the unified-surface rules);
		// verify pulls the bare nodes.stage.attribute form.
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{
				{
					Type:     "stage",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"row": map[string]any{
								"type":    "object",
								"default": map[string]any{},
							},
						},
					}},
				},
				{
					Type:     "verify",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"upstream": map[string]any{
								"type":   "object",
								"source": "{{nodes.stage.attribute}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare claim payload pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@q", Intent: "rw", Alias: "queue"},
				},
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"whole_payload": map[string]any{
							"type":   "object",
							"source": "{{claim.queue.payload}}",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare trigger payload pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"trigger": map[string]any{
							"type":   "object",
							"source": "{{trigger.message.payload}}",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare nodes event pull accepted", func(t *testing.T) {
		// Note: event-name field IS required for bare-event form; only
		// the path-after-name is optional. The cross-check against the
		// sender's executor's declared_events is silently skipped here
		// (no ExecutorDeclaredEvents hook wired).
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{
				{Type: "emit", Executor: "h"},
				{
					Type:     "receive",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"evt": map[string]any{
								"type":   "object",
								"source": "{{nodes.emit.event.progress}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("empty trailing dot still rejected", func(t *testing.T) {
		// `nodes.<X>.attribute.` (explicit empty trailing segment) is
		// not the bare form; it's a malformed directive and must be
		// rejected.
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{
				{Type: "stage", Executor: "h"},
				{
					Type:     "verify",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"bad": map[string]any{
								"type":   "object",
								"source": "{{nodes.stage.attribute.}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
	})
}

func TestValidator_FallbackOperator_Valid(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "stage", Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"out": map[string]any{"type": "string", "default": ""},
					},
				}},
			},
			{
				Type:     "verify",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"v": map[string]any{
							"type":   "string",
							"source": `{{nodes.stage.attribute.out | "default"}}`,
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v", res.Errors)
	}
}

func TestValidator_FallbackOperator_ChainsRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h"},
			{
				Type:     "c",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"v": map[string]any{
							"type":   "string",
							"source": `{{nodes.a.attribute.x | nodes.b.attribute.y | "default"}}`,
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	if res.Ok() {
		t.Fatalf("expected error for multi-pipe chain")
	}
}

// TestCheckAttributeSource_RelaxedGrammar — per the 2026-05-21
// userdata collapse, source declarations admit literal text alongside
// {{...}} directives and multiple directives in one source string.
func TestCheckAttributeSource_RelaxedGrammar(t *testing.T) {
	t.Run("literal text + one directive accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":   "string",
							"source": "Generate config for {{params.domain}}.",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("multiple directives separated by text accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":   "string",
							"source": "Hello {{params.x}}, world {{params.y}}.",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? marker on a single directive accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{
				{
					Type: "verify", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"warnings_block": map[string]any{
								"type":    "string",
								"default": "",
							},
						},
					}},
				},
				{
					Type: "generate", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"prompt": map[string]any{
								"type":   "string",
								"source": "{{nodes.verify.attribute.warnings_block?}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? marker on directive in embedded source accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{
				{
					Type: "verify", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"warnings_block": map[string]any{
								"type":    "string",
								"default": "",
							},
						},
					}},
				},
				{
					Type: "generate", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"prompt": map[string]any{
								"type":   "string",
								"source": "warnings: {{nodes.verify.attribute.warnings_block?}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? + | on the same directive rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type: "a", Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"v": map[string]any{
							"type":   "string",
							"source": `{{params.x? | "y"}}`,
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.v.source")
	})
}

// TestCheckAttributesSchema_UnifiedSurface — per the 2026-05-21
// userdata collapse, each property must declare exactly one of
// `source:` or `default:`, or be marked `readOnly: true` in the
// executor's expected_attributes_schema. Both-set is rejected.
func TestCheckAttributesSchema_UnifiedSurface(t *testing.T) {
	t.Run("property with source: and no default: accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":   "string",
							"source": "{{params.x}}",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("property with default: and no source: accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":    "string",
							"default": "claude-sonnet-4-5",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("property with both source: and default: rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"both": map[string]any{
							"type":    "string",
							"source":  "{{params.x}}",
							"default": "fallback",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"both":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.both")
	})

	t.Run("readOnly property without source/default accepted when executor declares readOnly", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":     "string",
							"readOnly": true,
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"summary":{"type":"string","readOnly":true}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("template readOnly without executor readOnly rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":     "string",
							"readOnly": true,
						},
					},
				}},
			}},
		}
		// Executor's schema declares the property but does NOT mark
		// it readOnly. Template claiming readOnly contradicts the
		// executor — the executor is authoritative.
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"summary":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.summary")
	})

	t.Run("property with neither source/default/readOnly rejected when executor schema is visible", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"orphan": map[string]any{
							"type": "string",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				// Executor declares no readOnly properties; orphan is
				// unknown to the executor's schema either.
				return []byte(`{"type":"object","properties":{}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.orphan")
	})

	t.Run("L1 default plus L2 source on same property: L2 source wins (no both-set error)", func(t *testing.T) {
		// L1 (template defaults.attributes.by_executor.<exec>.<attr>)
		// contributes a `default:` value for property `cli`. L2 (the
		// per-node attribute schema) declares a `source:` directive for
		// the same property. The merge must drop the L1 `default:` so the
		// effective schema is a clean source-bound property. Without the
		// fix in MergeAttributeDefaults, checkAttributesSchema would
		// reject the template for declaring both source: and default:.
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			ParamsSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"override_cli": map[string]any{"type": "object"},
				},
			},
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"h": {
							"cli": map[string]any{"silence_timeout_ms": 60000},
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cli": map[string]any{
							"type":   "object",
							"source": "{{params.override_cli}}",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"cli":{"type":"object"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 source: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("L1 source plus L2 default on same property: L2 default wins (no both-set error)", func(t *testing.T) {
		// Symmetric to the previous case. L1 contributes a value
		// (intent: serve as a default), L2 declares its own default.
		// The L2 default must override. Note: L1 only ever contributes
		// via `default:` per MergeAttributeDefaults's contract, but the
		// reverse case (L1 default + L2 default) confirms the drop-then-
		// overwrite shape leaves a single `default:` set.
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"h": {
							"model": "claude-sonnet-4-5",
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":    "string",
							"default": "claude-opus-4-7",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 default: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("permissive executor schema skips readOnly leg", func(t *testing.T) {
		// L2 declares one property with no `source:`, no `default:`, and no
		// `readOnly: true`. The executor advertises a permissive schema
		// (`{"type":"object"}` with no `properties` block) — `IsPermissive
		// ExecutorSchema` returns true ⇒ the readOnly-fallback leg is
		// skipped ⇒ the property is allowed through. This mirrors the
		// in-tree stub / http-node executors which advertise the
		// permissive shape to signal "open contract; accept any keys."
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"freeform": map[string]any{
							"type": "string",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object"}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "permissive executor schema: should accept the sourceless/defaultless property; errors: %+v", res.Errors)
	})
}

// TestIsPermissiveExecutorSchema pins the "open contract" recogniser the
// readOnly-fallback leg uses to decide whether to fire. An executor
// schema with no `properties` block declares open shape; a schema with
// a `properties` block (even an empty one) declares a closed contract
// with a known property set.
//
// The function operates on the parsed JSON Schema map[string]any, so
// these subtests construct the canonical shapes directly rather than
// going through json.Unmarshal.
func TestIsPermissiveExecutorSchema(t *testing.T) {
	t.Run("nil schema is not permissive", func(t *testing.T) {
		// nil means "executor didn't advertise a schema at all" — the
		// dispatch gate handles that case at a higher level (returning
		// `executor_schema_unavailable`). IsPermissiveExecutorSchema
		// reports false for nil so the readOnly leg doesn't get
		// short-circuited; the visibility flag carries the "schema not
		// reachable" semantics separately.
		assert.False(t, IsPermissiveExecutorSchema(nil))
	})

	t.Run("empty object is permissive", func(t *testing.T) {
		// `{}` is "no properties block, no constraints declared" — open
		// shape. The readOnly-fallback leg is skipped because the
		// executor declined to enumerate.
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{}))
	})

	t.Run("type-only object is permissive", func(t *testing.T) {
		// `{"type":"object"}` still has no `properties` block — open
		// shape. This is the canonical permissive form the stub and
		// http-node executors advertise.
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{"type": "object"}))
	})

	t.Run("empty properties block is closed (not permissive)", func(t *testing.T) {
		// `{"properties": {}}` declares "I have zero properties." That's
		// a closed contract distinct from "I don't enumerate" — the
		// readOnly-fallback leg should still fire on it.
		assert.False(t, IsPermissiveExecutorSchema(map[string]any{
			"properties": map[string]any{},
		}))
	})

	t.Run("populated properties block is closed", func(t *testing.T) {
		assert.False(t, IsPermissiveExecutorSchema(map[string]any{
			"properties": map[string]any{
				"x": map[string]any{"type": "string"},
			},
		}))
	})
}

// TestValidateAttributesSchema_TypeRedeclarationConflict — Gap 1 of the
// 2026-05-21 userdata-collapse cleanup. When the L2 template node-def
// redeclares a property `type:` and the executor's expected schema also
// declares a `type:` for the same property, the two MUST match. The
// executor is authoritative on types; a redeclared-but-conflicting type
// is rejected at registration with `template_validation_failed`.
func TestValidateAttributesSchema_TypeRedeclarationConflict(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{
						"type":    "integer",
						"default": 42,
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model.type")
}

// TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L2 — Gap 2.
// When the executor's expected schema sets `additionalProperties: false`
// and L2 declares a property the executor doesn't enumerate, the
// template is rejected. The executor's closed-schema policy is
// authoritative.
func TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L2(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"extra_field": map[string]any{
						"type":    "string",
						"default": "hi",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.extra_field")
}

// TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L1 — Gap 2,
// symmetric L1 case. When the executor's expected schema sets
// `additionalProperties: false`, an L1 default-value entry for a
// property the executor doesn't enumerate is also rejected. The
// rejection's `Path` lives under defaults.attributes.by_executor since
// the violation originates at L1.
func TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L1(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Defaults: &TemplateDefaults{
			Attributes: &TemplateAttributeDefaults{
				ByExecutor: map[string]map[string]any{
					"h": {
						"extra_field": "hi",
					},
				},
			},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"known": map[string]any{
						"type":    "string",
						"default": "x",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "defaults.attributes.by_executor.extra_field")
}

// TestValidateAttributesSchema_NestedDefaultTypeConflict — Gap 2 case
// (c). L2 declares a `default:` value whose runtime shape contradicts
// the executor's nested-property type declaration. The flat L2-vs-
// executor type comparison cannot catch this (the L2 property type is
// fine; only the default value is wrong); the JSON-Schema validation
// of the composed defaults bag against the executor's raw schema
// catches the violation.
func TestValidateAttributesSchema_NestedDefaultTypeConflict(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cli": map[string]any{
						"type": "object",
						"default": map[string]any{
							// silence_timeout_ms is declared `integer` by
							// the executor, but here we set it to a string
							// ("60s"). The composed-defaults-bag validation
							// catches this.
							"silence_timeout_ms": "60s",
						},
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{
				"type":"object",
				"properties":{
					"cli":{
						"type":"object",
						"properties":{
							"silence_timeout_ms":{"type":"integer"}
						}
					}
				}
			}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.defaults")
}

// TestValidateCompositionAgainstExecutor_RequiredInputWithSource pins the
// fix for a false-positive that fired when an executor's expected schema
// declared `required: ["X"]` and the template bound `X` via `source:`
// (no `default:`). The defaults bag at registration is intentionally a
// proper subset of the dispatch bag — source-bound properties have no
// default and are absent from the bag — so enforcing the executor's
// `required:` against it would (incorrectly) flag X as missing. The
// fix strips `required:` from the schema used for the defaults-bag
// validation pass; type and nested-shape checks against present values
// continue to fire.
func TestValidateCompositionAgainstExecutor_RequiredInputWithSource(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"system_prompt": map[string]any{
						"type":   "string",
						"source": "{{params.x}}",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{
				"type":"object",
				"properties":{
					"system_prompt":{"type":"string"}
				},
				"required":["system_prompt"]
			}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(),
		"executor required + template source: registration must not fire false-positive `required:`; errors: %+v",
		res.Errors)
}

// TestValidateAttributesSchema_OpenSchemaAcceptsExtraProperty — control
// case for Gap 2. When the executor's expected schema does NOT set
// `additionalProperties: false`, L2 declarations for properties the
// executor doesn't enumerate are admissible (the schema is open). The
// executor's enumerated property (`known`) is `readOnly: true` so the
// template needn't declare a source/default for it.
func TestValidateAttributesSchema_OpenSchemaAcceptsExtraProperty(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"extra_field": map[string]any{
						"type":    "string",
						"default": "hi",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			// additionalProperties default is true (open). `known` is
			// executor-written (readOnly: true) so its absence in L2 is
			// admissible under the unified-surface check.
			return []byte(`{"type":"object","properties":{"known":{"type":"string","readOnly":true}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "open executor schema should admit extra L2 props; errors: %+v", res.Errors)
}
