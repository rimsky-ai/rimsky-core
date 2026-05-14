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
