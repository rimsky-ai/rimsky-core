// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var knownClaimProducers = map[string]string{
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
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_SubscribeToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ghost", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

func TestValidateTemplate_Ok_SubscribeToMessageTypeShapedNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/recheck"}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_SubscribeToUndeclaredMessageType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

func TestValidateSubscribes_Ok_MessageVirtualNodeWhenBodyField(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type:       "ping/recheck",
			BodySchema: []byte(`{"type":"object","properties":{"pong_status":{"type":"string"}}}`),
		}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.pong_status == "ok"`, ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_Error_MessageVirtualNodeWhenUnknownBodyField(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type:       "ping/recheck",
			BodySchema: []byte(`{"type":"object","properties":{"pong_status":{"type":"string"}}}`),
		}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.pongStatus == "ok"`},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].when")
}

func TestValidateSubscribes_Error_MessageVirtualNodeWhenEmptyBodySchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/recheck"}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.anything == "ok"`},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].when")
}

func TestValidateClaimProducers_Ok_RegionClaimWithIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Selector: "/data/x", Intent: "rw"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateClaimProducers_Error_MissingIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:           "a",
			Executor:       "h",
			ClaimProducers: []NodeClaimProducerRef{{Name: "content", Selector: "/data/x"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].intent")
}

func TestValidateClaimProducers_Error_DuplicateAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Selector: "/x", Intent: "r", Alias: "shared"},
				{Name: "shared", Selector: "/y", Intent: "r", Alias: "shared"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[1].alias")
}

func TestValidateSubstitutionRef_ClaimProducerSelectorOriginNotMisreportedAsAttributesSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Intent: "r", Selector: "{{nodes.ghost.attribute.x}}"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].selector (substitution ref)")
}

func TestValidateClaimProducers_Error_UnknownStoreKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:           "a",
			Executor:       "h",
			ClaimProducers: []NodeClaimProducerRef{{Name: "ghost", Selector: "/x", Intent: "r"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].name")
}

func TestHoldingSubgraphsForTemplate_HeldChain(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "pick",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:       "process",
				Subscribes: []SubscriptionEntry{{Node: "pick", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}},
				Holds: map[string]HoldsBinding{
					"queue": {From: "pick"},
				},
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
				ClaimProducers: []NodeClaimProducerRef{
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
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:    storeDeclaredLookup(knownClaimProducers),
		ExecutorDeclared: func(name string) bool { return name == "handler.a" },
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_ExecutorDeclared_Missing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "claude-agent",
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:    storeDeclaredLookup(knownClaimProducers),
		ExecutorDeclared: func(name string) bool { return false },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].executor")
	var msg string
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[0].executor") {
			msg = e.Msg
			break
		}
	}
	require.Contains(t, msg, "claude-agent")
}

func TestValidateTemplate_ClaimScopeSpelling(t *testing.T) {
	makeSpec := func(directive string) *TemplateSpec {
		return &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "worker",
				Executor: "handler.worker",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "a", Intent: "rw", Selector: "/scope-A"},
				},
				Attributes: &NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"region": map[string]any{
								"type":   "string",
								"source": directive,
							},
						},
					},
				},
			}},
		}
	}

	resCanonical := ValidateTemplate(
		makeSpec("{{claim.a.claim_scope}}"),
		RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)},
	)
	assert.True(t, resCanonical.Ok(),
		"canonical {{claim.a.claim_scope}} must validate; errors: %+v", resCanonical.Errors)

	resLegacy := ValidateTemplate(
		makeSpec("{{claim.a.scope}}"),
		RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)},
	)
	require.False(t, resLegacy.Ok(),
		"legacy {{claim.a.scope}} must be rejected at registration")
	hasErrorAt(t, resLegacy, "nodes[0].attributes.schema.properties.region.source")

	var legacyMsg string
	for _, e := range resLegacy.Errors {
		if strings.HasPrefix(e.Path, "nodes[0].attributes.schema.properties.region.source") {
			legacyMsg = e.Msg
			break
		}
	}
	require.Contains(t, legacyMsg, "claim_scope",
		"the legacy-spelling rejection must name the canonical claim_scope segment; got %q", legacyMsg)
}

func TestValidator_WarnsOnMissingAcquireUnavailablePolicy(t *testing.T) {
	t.Run("stores_no_policy_warns", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{
				Type: "a",
				ClaimProducers: []NodeClaimProducerRef{
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
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{
				Type: "a",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
				ErrorTypes: map[string]ErrorTypePolicy{
					"acquire/unavailable": {
						Action: "give_up",
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
			Name: "demo", Version: "1",
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

func TestValidateErrorTypes_RejectsUnknown(t *testing.T) {
	retiredNames := []string{
		"invalidate",
		"resume_then_retry",
		"discard_then_retry",
		"discard_claims_then_retry",
		"foo",
	}
	for _, action := range retiredNames {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Action: action},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, "nodes[1].error_types[some_error].action")
		})
	}
}

func TestValidateErrorTypes_AcceptsCanonical(t *testing.T) {
	for _, action := range []string{"pass", "give_up", "retry", "release_and_requeue"} {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Action: action},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			for _, e := range res.Errors {
				if e.Path == "nodes[1].error_types[some_error].action" {
					t.Fatalf("unexpected action-vocabulary error for %q: %s", action, e.Msg)
				}
			}
		})
	}
}

func TestValidateErrorTypes_AcceptsDeclaredHttpClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/timeout": {Action: "give_up"},
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

func TestValidateErrorTypes_AcceptsDeclaredWildcardClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/server_error/500": {Action: "retry"},
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

func TestValidateErrorTypes_AcceptsRuntimeSynthesizedExecutorSyncTimeout(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"executor_sync_timeout": {Action: "retry"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, w := range res.Warnings {
		if strings.HasPrefix(w.Path, "nodes[1].error_types") {
			t.Fatalf("executor_sync_timeout is runtime-synthesized (runner_dispatch.go) and must not warn as undeclared: %+v", w)
		}
	}
}

func TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Action: "give_up"},
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

func TestValidateErrorTypes_WarnsUndeclaredWhenHookAvailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Action: "give_up"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "unattributable error_types key must not hard-reject; errors: %+v", res.Errors)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[foo]" {
			found = true
			if !strings.Contains(w.Msg, "not in any declared vocabulary") {
				t.Fatalf("warning must state no declared vocabulary contains the key; got %q", w.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("expected advisory warning at nodes[1].error_types[foo]; warnings: %+v", res.Warnings)
	}
}

func TestValidateErrorTypes_AcceptsProducerDeclaredClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http",
				ClaimProducers: []NodeClaimProducerRef{{Name: "items-store", Alias: "items", Intent: "rw", Selector: "items?category=alpha"}},
				ErrorTypes: map[string]ErrorTypePolicy{
					"pg/claim_unavailable": {Action: "retry"},
				}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		StoreDeclared:                func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
		StoreDeclaredErrorClasses: func(name string) ([]string, bool) {
			if name == "items-store" {
				return []string{"pg/claim_unavailable", "pg/swap_failed"}, true
			}
			return nil, false
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "producer-declared key must validate; errors: %+v", res.Errors)
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[pg/claim_unavailable]" {
			t.Fatalf("producer-declared key must be hard-valid, not a warning: %+v", w)
		}
	}
}

func TestValidateErrorTypes_ProducerClassUnreachableFromNode(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"pg/claim_unavailable": {Action: "retry"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
		StoreDeclaredErrorClasses: func(string) ([]string, bool) {
			return []string{"pg/claim_unavailable"}, true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "must warn, not reject; errors: %+v", res.Errors)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[pg/claim_unavailable]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for producer class unreachable from node; warnings: %+v", res.Warnings)
	}
}

func TestValidateSubscribes_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_SelfOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "drainer", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "drainer", Type: "terminal/success", When: "payload.changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_SelfBareOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "loopy", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_SelfWithFrameInExplicitOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "loopy", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_RejectsBareEvent(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "event", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateSubscribes_RejectsUnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "garbage/foo", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateSubscribes_RejectsTransientType(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "transient/retry/*", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "transient/retry/*") {
			found = true
		}
	}
	require.True(t, found, "expected validation error naming the rejected transient subscription type, got %+v", res.Errors)
}

func TestValidateSubscribes_RejectsMalformedCEL(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: "payload.foo &&&", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateSubscribes_RejectsMissingForceUpstreamRefresh(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "missing force_upstream_refresh must be rejected")
	found := false
	for _, e := range res.Errors {
		if strings.HasSuffix(e.Path, ".force_upstream_refresh") && strings.Contains(e.Msg, "required") {
			found = true
			break
		}
	}
	require.True(t, found, "expected an error whose path ends in .force_upstream_refresh with a required message; got %+v", res.Errors)
}

func TestValidateSubscribes_RejectsConflictingFlagsOnSameKey(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(true)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "conflicting cascade-shape flag values on the same subscription key must be rejected")
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "conflicting cascade-shape flags") &&
			strings.HasSuffix(e.Path, ".subscribes[1]") {
			found = true
			break
		}
	}
	require.True(t, found, "expected a conflicting-cascade-shape-flags error on subscribes[1]; got %+v", res.Errors)
}

func TestValidateSubscribes_AllowsExactDuplicateFlags(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "conflicting cascade-shape flags") {
			t.Fatalf("exact-duplicate entries must not trigger the conflict check; got %+v", res.Errors)
		}
	}
}

func TestValidateCascadeMode_Ok(t *testing.T) {
	for _, mode := range []string{"", "most-recent", "sequenced", "idempotent-queue", "idempotent-settled"} {
		t.Run(mode, func(t *testing.T) {
			spec := &TemplateSpec{
				Name:    "demo",
				Version: "1.0.0",
				Nodes: []TemplateNodeDef{{
					Type: "a", Executor: "h", CascadeMode: mode,
				}},
			}
			res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
			assert.True(t, res.Ok(), "errors: %+v", res.Errors)
		})
	}
}

func TestValidateCascadeMode_Unknown(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type: "a", Executor: "h", CascadeMode: "bogus",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].cascade_mode")
}

func TestTemplateValidator_DefaultsByExecutor(t *testing.T) {
	t.Run("unknown executor name is rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"unknown-executor": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, `defaults.attributes.by_executor["unknown-executor"]`)
	})

	t.Run("matching executor name is accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("fragment values are not inspected (only routing keys)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {
							"garbage_key": []any{"a", 1, true, nil, map[string]any{"k": "v"}},
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})
}

func TestTemplateValidator_Tags(t *testing.T) {
	t.Run("valid params reference accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("unknown params key rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].tags[0]")
	})

	t.Run("unsupported kind in tag rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"{{claim.staging.address}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].tags[0]")
	})

	t.Run("plain string tag accepted (no directives)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"setup"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("subscription When payload.tags literal not in sender's declared_tags rejects registration", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "a", Executor: "h"},
				{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: `"undeclared_tag" in payload.tags`, ForceUpstreamRefresh: BoolPtr(false)},
				}},
			},
		}
		hooks := RegistryHooks{
			ExecutorDeclaredTags: func(string) ([]string, bool) { return []string{"declared_tag"}, true },
		}
		res := ValidateTemplate(spec, hooks)
		found := false
		for _, e := range res.Errors {
			if e.Path == "nodes[1].subscribes[0].when" && strings.Contains(e.Msg, "undeclared_tag") {
				found = true
			}
		}
		require.True(t, found, "expected an error naming the undeclared tag at nodes[1].subscribes[0].when; errors: %+v", res.Errors)
		require.False(t, res.Ok(), "registration must reject an undeclared subscription tag, not just warn")
	})

	t.Run("subscription When payload.tags literal in sender's declared_tags does not reject", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "a", Executor: "h"},
				{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: `"declared_tag" in payload.tags`, ForceUpstreamRefresh: BoolPtr(false)},
				}},
			},
		}
		hooks := RegistryHooks{
			ExecutorDeclaredTags: func(string) ([]string, bool) { return []string{"declared_tag"}, true },
		}
		res := ValidateTemplate(spec, hooks)
		for _, e := range res.Errors {
			if e.Path == "nodes[1].subscribes[0].when" {
				t.Fatalf("unexpected undeclared-tag error for a declared literal: %+v", e)
			}
		}
	})
}

func TestCheckAttributeSource_BareFormPulls(t *testing.T) {
	t.Run("bare nodes attribute pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
					Subscribes: []SubscriptionEntry{
						{Node: "stage", Type: "attribute/*", ForceUpstreamRefresh: BoolPtr(false)},
					},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare claim payload pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare nodes event pull rejected (retired)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.False(t, res.Ok(), "expected validator to reject the retired event source-kind")
	})

	t.Run("empty trailing dot still rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "stage", Executor: "h"},
				{
					Type:     "verify",
					Executor: "h",
					Subscribes: []SubscriptionEntry{
						{Node: "stage", Type: "attribute/*", ForceUpstreamRefresh: BoolPtr(false)},
					},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		found := false
		for _, e := range res.Errors {
			if strings.Contains(e.Msg, "empty trailing segment") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected diagnostic naming the empty trailing segment, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
		}
	})
}

func TestValidator_FallbackOperator_Valid(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
				Subscribes: []SubscriptionEntry{
					{Node: "stage", Type: "attribute/out/changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
	}
}

func TestValidator_FallbackOperator_ChainsRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "default": ""},
					},
				}},
			},
			{Type: "b", Executor: "h"},
			{
				Type:     "c",
				Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(), "expected error for multi-pipe chain")
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "multi-pipe fallback chain") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic naming the multi-pipe fallback chain, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
	}
}

func TestCheckAttributeSource_RelaxedGrammar(t *testing.T) {
	t.Run("literal text + one directive accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("multiple directives separated by text accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? marker on a single directive accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
					Subscribes: []SubscriptionEntry{
						{Node: "verify", Type: "attribute/warnings_block/changed", ForceUpstreamRefresh: BoolPtr(false)},
					},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? marker on directive in embedded source accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
					Subscribes: []SubscriptionEntry{
						{Node: "verify", Type: "attribute/warnings_block/changed", ForceUpstreamRefresh: BoolPtr(false)},
					},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? + | on the same directive rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.v.source")
	})
}

func TestCheckAttributesSchema_UnifiedSurface(t *testing.T) {
	t.Run("property with source: and no default: accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("property with default: and no source: accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("property with both source: and default: rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
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
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"summary":{"type":"string","readOnly":true}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("template readOnly without executor readOnly rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
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
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.orphan")
	})

	t.Run("extension property without source/default/readOnly accepted when executor declares additionalProperties:true", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"zone_codes": map[string]any{
							"type": "array",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string","default":"claude-sonnet-4-5"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("extension property marked readOnly accepted when executor declares additionalProperties:true", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"notes": map[string]any{
							"type":     "string",
							"readOnly": true,
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string","default":"claude-sonnet-4-5"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("ENUMERATED property still requires source/default/readOnly under additionalProperties:true", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type": "string",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model")
	})

	t.Run("L1 default plus L2 source on same property: L2 source wins (no both-set error)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"cli":{"type":"object"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 source: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("L1 source plus L2 default on same property: L2 default wins (no both-set error)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 default: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("permissive executor schema skips readOnly leg", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
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
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object"}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "permissive executor schema: should accept the sourceless/defaultless property; errors: %+v", res.Errors)
	})
}

func TestIsPermissiveExecutorSchema(t *testing.T) {
	t.Run("nil schema is not permissive", func(t *testing.T) {
		assert.False(t, IsPermissiveExecutorSchema(nil))
	})

	t.Run("empty object is permissive", func(t *testing.T) {
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{}))
	})

	t.Run("type-only object is permissive", func(t *testing.T) {
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{"type": "object"}))
	})

	t.Run("empty properties block is closed (not permissive)", func(t *testing.T) {
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

func TestValidateAttributesSchema_TypeRedeclarationConflict(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model.type")
}

func TestValidateAttributesSchema_NestedSourceGrammarValidated(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"nested_field": map[string]any{
								"type":   "string",
								"source": "claim.unknown_alias.payload",
							},
						},
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(), "nested source directive with an unresolvable claim alias must be rejected at registration, not just at dispatch")
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.config.properties.nested_field.source")
}

func TestValidateAttributesSchema_NestedSourceNonStringRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"nested_field": map[string]any{
								"type":   "string",
								"source": []any{"{{params.a}}", "{{params.b}}"},
							},
						},
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.config.properties.nested_field.source")
}

func TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L2(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.extra_field")
}

func TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L1(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "defaults.attributes.by_executor.extra_field")
}

func TestValidateAttributesSchema_NestedDefaultTypeConflict(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cli": map[string]any{
						"type": "object",
						"default": map[string]any{
							"silence_timeout_ms": "60s",
						},
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
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

func TestValidateCompositionAgainstExecutor_RequiredInputWithSource(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
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

func TestValidateAttributesSchema_OpenSchemaAcceptsExtraProperty(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string","readOnly":true}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "open executor schema should admit extra L2 props; errors: %+v", res.Errors)
}

func TestValidateTemplate_ReferenceValidationIsUnconditionallyStrict(t *testing.T) {
	const notProvisioned = "ghost-executor"
	const provisionedConstrained = "constrained-executor"
	const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorDeclared: func(name string) bool {
			return name == provisionedConstrained
		},
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			if name == provisionedConstrained {
				return []byte(constrainedSchema), true
			}
			return nil, false
		},
	}

	notProvisionedNode := func() TemplateNodeDef {
		return TemplateNodeDef{Type: "ghost", Executor: notProvisioned}
	}

	invalidProvisionedNode := func() TemplateNodeDef {
		return TemplateNodeDef{
			Type:     "constrained",
			Executor: provisionedConstrained,
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{
						"type":    "integer",
						"default": -1,
					},
				},
			}},
		}
	}

	specWith := func(nodes ...TemplateNodeDef) *TemplateSpec {
		return &TemplateSpec{
			Name:    "ref-mode-demo",
			Version: "1",
			Nodes:   nodes,
		}
	}

	t.Run("not-provisioned executor ref hard-fails", func(t *testing.T) {
		spec := specWith(notProvisionedNode())
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok(),
			"a reference to a not-yet-provisioned executor must be rejected; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[0].executor")
	})

	t.Run("provisioned but schema-invalid ref hard-fails", func(t *testing.T) {
		spec := specWith(invalidProvisionedNode())
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok(),
			"a genuinely-invalid provisioned ref must always be rejected; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[0].attributes")
	})

	t.Run("executor's expected_attributes_schema not visible at registration hard-fails", func(t *testing.T) {
		spec := specWith(TemplateNodeDef{
			Type:     "unseen",
			Executor: notProvisioned,
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
			}},
		})
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes")
	})
}

func TestValidateMessages_Ok_DeclaredTypeAndBodySchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {"reason": {"type": "string"}},
					"required": ["reason"]
				}`),
			},
			{Type: "alert/fire"},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// @decision: empty-message-as-root-trigger
func TestValidateMessages_Error_EmptyType(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: ""}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	foundReservation := false
	for _, e := range res.Errors {
		if e.Path == "messages[0].type" && strings.Contains(e.Msg, "reserved-for-runtime") {
			foundReservation = true
			break
		}
	}
	require.True(t, foundReservation, "expected reserved-for-runtime message; got %+v", res.Errors)
}

func TestValidateMessages_Ok_NoEmptyDeclaration(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type": "object"}`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateMessages_Error_DuplicateType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck"},
			{Type: "ping/recheck"},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[1].type")
}

func TestValidateMessages_Error_TypeWithWhitespace(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping recheck"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

func TestValidateMessages_Error_TypeTrailingSlash(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

func TestValidateMessages_Error_TypeMustBeSlashBearing(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "invalidate"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

func TestValidateMessages_Error_BodySchemaNotJSON(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{not-json`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

func TestValidateMessages_Error_BodySchemaScalar(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`"a-string"`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

func TestValidateMessages_Error_BodySchemaInvalidSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type": "not-a-real-json-schema-type"}`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

func TestValidateMessageSubstitutionRef_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {"reason": {"type": "string"}, "pong_status": {"type": "string"}}
				}`),
			},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{
					Node:                 "ping/recheck",
					Type:                 "terminal/success",
					ForceUpstreamRefresh: BoolPtr(false),
				},
			},
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
}

func TestValidateMessageSubstitutionRef_Error_UnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.other/type.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func TestValidateMessageSubstitutionRef_Error_UnknownField(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.no_such_field}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func TestValidateMessageSubstitutionRef_Ok_BareWholeBodyPull(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{
					Node:                 "ping/recheck",
					Type:                 "terminal/success",
					ForceUpstreamRefresh: BoolPtr(false),
				},
			},
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"whole_body": map[string]any{
						"type":   "object",
						"source": "{{messages.ping/recheck}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
}

func TestValidateMessageSubstitutionRef_Error_NamedFieldAgainstEmptyBody(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck"},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func sendsMessageOKSpec(t *testing.T) *TemplateSpec {
	t.Helper()
	return &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type: "ping/recheck",
			BodySchema: []byte(`{
				"type": "object",
				"properties": {
					"pong_status": {"type": "string"}
				},
				"required": ["pong_status"]
			}`),
		}},
		Nodes: []TemplateNodeDef{{
			Type:         "ping-recheck-emitter",
			SendsMessage: "ping/recheck",
			Attributes: &NodeAttributesDef{
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pong_status": map[string]any{"type": "string"},
					},
					"required": []any{"pong_status"},
				},
			},
		}},
	}
}

func TestValidateTemplate_Ok_SendsMessage_ExactShape(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_UnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:         "bad-emitter",
			SendsMessage: "unknown/type",
			Attributes:   &NodeAttributesDef{Schema: map[string]any{}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].sends_message")
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "unknown message type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an 'unknown message type' diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_MutexWithExecutor(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Executor = "handler.a"
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "sends_message and executor are mutually exclusive") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected mutual-exclusion error executor vs sends_message, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_MutexWithDelegate(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Delegate = "sub"
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "sends_message and delegate are mutually exclusive") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected mutual-exclusion error delegate vs sends_message, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_AttributeSuperset(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "string"},
			"extra_field": map[string]any{"type": "string"},
		},
		"required": []any{"pong_status"},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "extra_field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic naming the superset field 'extra_field', got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_AttributeSubset_MissingField(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "is missing field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic about missing destination field, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_AttributeTypeMismatch(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "integer"},
		},
		"required": []any{"pong_status"},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "types must match exactly") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected per-property type-mismatch diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_RequiredMismatch(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "string"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "required: sets must match exactly") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected required-set mismatch diagnostic, got %+v", res.Errors)
	}
}

func coalesceWarningSpec(mode string, types ...string) *TemplateSpec {
	msgs := make([]MessageSchema, 0, len(types))
	for _, tp := range types {
		msgs = append(msgs, MessageSchema{Type: tp})
	}
	return &TemplateSpec{
		Name:             "demo",
		Version:          "1.0.0",
		MessageQueueMode: mode,
		Messages:         msgs,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
}

func coalesceCrossTypeWarnings(res ValidationResult) []ValidationWarning {
	var out []ValidationWarning
	for _, w := range res.Warnings {
		if w.Path == "message_queue_mode" {
			out = append(out, w)
		}
	}
	return out
}

func TestValidateMessageQueueMode_Warning_CoalesceWithMultipleTypes(t *testing.T) {
	spec := coalesceWarningSpec("coalesce", "job/start", "job/stop")
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "warning must not block registration; errors: %+v", res.Errors)
	warns := coalesceCrossTypeWarnings(res)
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0].Msg, "cancels ALL")
	assert.Contains(t, warns[0].Msg, "job/start")
	assert.Contains(t, warns[0].Msg, "job/stop")
}

func TestValidateMessageQueueMode_NoWarning_CoalesceSingleType(t *testing.T) {
	spec := coalesceWarningSpec("coalesce", "job/start")
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	assert.Empty(t, coalesceCrossTypeWarnings(res))
}

func TestValidateMessageQueueMode_NoWarning_BacklogWithMultipleTypes(t *testing.T) {
	spec := coalesceWarningSpec("backlog", "job/start", "job/stop")
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	assert.Empty(t, coalesceCrossTypeWarnings(res))
}
