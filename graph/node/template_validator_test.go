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
				{Node: "ghost", On: "state"},
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
				Subscribes: []SubscriptionEntry{{Node: "pick", On: "state"}},
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
				Subscribes: []SubscriptionEntry{{Node: "pick", On: "state"}},
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
					{Node: "pick_a", On: "state"},
					{Node: "pick_b", On: "state"},
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
				Subscribes: []SubscriptionEntry{{Node: "pick", On: "state"}},
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

// --- Lifecycle handler validation tests ---

func TestValidateTemplate_OnAcquireUnavailable_Pass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:                 "a",
			OnAcquireUnavailable: &OnAcquireUnavailableHandler{Resolve: ResolvePass},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_OnAcquireUnavailable_BadResolve(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:                 "a",
			OnAcquireUnavailable: &OnAcquireUnavailableHandler{Resolve: "bogus"},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].on_acquire_unavailable.resolve")
}

func TestValidateTemplate_OnAcquireUnavailable_ErrorMissingClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:                 "a",
			OnAcquireUnavailable: &OnAcquireUnavailableHandler{Resolve: ResolveError},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].on_acquire_unavailable.error_class")
}

func TestValidateTemplate_OnAcquireUnavailable_ErrorClassUnknown(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a",
			OnAcquireUnavailable: &OnAcquireUnavailableHandler{
				Resolve: ResolveError, ErrorClass: "not_declared",
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].on_acquire_unavailable.error_class")
}

func TestValidateTemplate_OnExecutorComplete_AlwaysPropagate(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:               "a",
			OnExecutorComplete: &OnExecutorCompleteHandler{Resolve: ResolveAlwaysPropagate},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_OnExecutorComplete_BadResolve(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:               "a",
			OnExecutorComplete: &OnExecutorCompleteHandler{Resolve: "bogus"},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].on_executor_complete.resolve")
}

func TestValidateTemplate_OnExecutorErrored_BadResolve(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:              "a",
			OnExecutorErrored: &OnExecutorTerminalHandler{Resolve: "bogus"},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].on_executor_errored.resolve")
}

// TestValidateTemplate_EmptyHandlerRejected: handlers must declare a
// resolve verb. The invalidate-emit slot retired post-2026-05-14, so
// the "neither resolve nor invalidate" case collapses to "resolve is
// required."
func TestValidateTemplate_EmptyHandlerRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:                 "a",
			OnAcquireUnavailable: &OnAcquireUnavailableHandler{},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].on_acquire_unavailable")
}

// TestValidateTemplate_PolicyAction_InvalidateRejected: error-policy
// `action: invalidate` retires per the 2026-05-14 subscription-cascade
// resolution; receivers declare cascade coupling via Subscribes.
func TestValidateTemplate_PolicyAction_InvalidateRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
				"some_error": {Policy: []PolicyAction{
					{Action: "invalidate", Targets: []string{"a"}, Frame: FrameIn},
				}},
			}},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].error_types[some_error].policy[0].action")
}

// TestValidateSubscribes_Ok covers the happy path: a state-when-fresh
// subscription against a declared node.
func TestValidateSubscribes_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", On: "state", When: "fresh"},
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
					{Node: "a", Instance: true, On: "state"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_EventNameRequired: on:event needs a name.
func TestValidateSubscribes_EventNameRequired(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1", FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", On: "event"},
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

func TestValidateUserdataSchema_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Userdata: map[string]any{"max_tokens": 1024},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorUserdataSchema: func(name string) ([]byte, bool) {
			return []byte(`{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"type": "object",
				"properties": { "max_tokens": { "type": "integer" } }
			}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateUserdataSchema_Violation(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Userdata: map[string]any{"max_tokens": "should-be-int"},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorUserdataSchema: func(name string) ([]byte, bool) {
			return []byte(`{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"type": "object",
				"properties": { "max_tokens": { "type": "integer" } },
				"required": ["max_tokens"]
			}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].userdata")
}

// TestTemplateValidator_DefaultsByExecutor covers the per-spec routing-
// key cross-check on `defaults.userdata.by_executor.<name>` (template-
// level userdata defaults per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 1).
func TestTemplateValidator_DefaultsByExecutor(t *testing.T) {
	t.Run("unknown executor name is rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Userdata: &TemplateUserdataDefaults{
					ByExecutor: map[string]map[string]any{
						"unknown-executor": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, `defaults.userdata.by_executor["unknown-executor"]`)
	})

	t.Run("matching executor name is accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Userdata: &TemplateUserdataDefaults{
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
		// userdata-inertness invariant says we never read fragment values.
		spec := &TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: FrameResolutionSerialQueue,
			Defaults: &TemplateDefaults{
				Userdata: &TemplateUserdataDefaults{
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
							"row": map[string]any{"type": "object"},
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
						"out": map[string]any{"type": "string"},
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
