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

func TestValidateHolds_FromUndeclared(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Holds: map[string]HoldsBinding{
					"target": {From: "ghost"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].holds[target].from")
	if !findErrorContains(res.Errors, "holds_from_undeclared") {
		t.Fatalf("expected holds_from_undeclared error, got %+v", res.Errors)
	}
}

func TestValidateHolds_SelfReferenceRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "consumer"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(), "a node holding a claim from itself is not an upstream dependency of itself")
	hasErrorAt(t, res, "nodes[0].holds[shared_thing].from")
	if !findErrorContains(res.Errors, "holds_from_not_dependency") {
		t.Fatalf("expected holds_from_not_dependency error, got %+v", res.Errors)
	}
}

func TestValidateHoldsAcyclic_TwoNodeCycleRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "handler.a",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "a_claim", Intent: "rw", Selector: "{{params.s}}"},
				},
				Holds: map[string]HoldsBinding{
					"b_claim": {From: "b"},
				},
			},
			{
				Type:     "b",
				Executor: "handler.b",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "b_claim", Intent: "rw", Selector: "{{params.s}}"},
				},
				Holds: map[string]HoldsBinding{
					"a_claim": {From: "a"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(), "a two-node holds: cycle (a holds from b, b holds from a) must be rejected — co-holdership must be acyclic")
	if !findErrorContains(res.Errors, "holds: cycle") {
		t.Fatalf("expected a holds-cycle error, got %+v", res.Errors)
	}
}

func TestValidateHolds_DisconnectedFromButAcyclicIsLegal(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "signaler",
				Executor: "handler.signaler",
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Subscribes: []SubscriptionEntry{
					{Node: "signaler", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "producer"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(),
		"holds: from a node the holder does not subscribe to (mixed-upstream wedge diagnostics pattern) must remain legal as long as it is acyclic; errors: %+v", res.Errors)
}

func TestValidateHolds_AsRemapCollisionRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer_a",
				Executor: "handler.producer_a",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "claim_a", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "producer_b",
				Executor: "handler.producer_b",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "claim_b", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Subscribes: []SubscriptionEntry{
					{Node: "producer_a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
					{Node: "producer_b", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"claim_a": {From: "producer_a", As: "shared_local_name"},
					"claim_b": {From: "producer_b", As: "shared_local_name"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(),
		"two holds: entries resolving to the same local alias must be rejected — the runtime keys co-held claims by that alias and would silently drop one")
	if !findErrorContains(res.Errors, "holds_local_alias_collision") {
		t.Fatalf("expected holds_local_alias_collision error, got %+v", res.Errors)
	}
}

func TestValidateHolds_UnknownClaimAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Holds: map[string]HoldsBinding{
					"nonexistent": {From: "producer"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].holds[nonexistent]")
}

func TestValidateHolds_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Subscribes: []SubscriptionEntry{
					{Node: "producer", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "producer"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateHolds_ClaimReadFromHeldAliasOk(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Subscribes: []SubscriptionEntry{
					{Node: "producer", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "producer"},
				},
				Attributes: &NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"addr": map[string]any{
								"type":   "string",
								"source": "{{claim.shared_thing.address}}",
							},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateFanOut_HeldAliasResolvesProducerForSplitScopeCheck(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "worker",
				Executor: "handler.worker",
				Subscribes: []SubscriptionEntry{
					{Node: "producer", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "producer"},
				},
				FanOut: &FanOutSpec{
					Claim:            "shared_thing",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared:                     storeDeclaredLookup(knownClaimProducers),
		ClaimProducerAdvertisesSplitScope: func(name string) bool { return false },
	})
	require.False(t, res.Ok(),
		"fan_out over a holds:-aliased claim must resolve the upstream producer and enforce split-scope, not silently no-op")
	if !findErrorContains(res.Errors, "supports_split_scope") {
		t.Fatalf("expected split-scope enforcement error naming supports_split_scope, got %+v", res.Errors)
	}
}

func TestValidateHolds_AsRemapAdmitsLocalAliasNotMapKey(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Subscribes: []SubscriptionEntry{
					{Node: "producer", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "producer", As: "local_name"},
				},
				Attributes: &NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"addr": map[string]any{
								"type":   "string",
								"source": "{{claim.local_name.address}}",
							},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(),
		"a source directive referencing the holds: `as:` local rename must validate (runtime keys the co-held claim map by `as:`, not the map key); errors: %+v", res.Errors)
}

func TestValidateHolds_MapKeyRejectedAtRuntimeAliasWhenAsRemapSet(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "shared_thing", Intent: "rw", Selector: "{{params.s}}"},
				},
			},
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Subscribes: []SubscriptionEntry{
					{Node: "producer", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Holds: map[string]HoldsBinding{
					"shared_thing": {From: "producer", As: "local_name"},
				},
				Attributes: &NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"addr": map[string]any{
								"type":   "string",
								"source": "{{claim.shared_thing.address}}",
							},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(),
		"the raw holds: map key must NOT validate as a claim.<alias> reference once `as:` remaps the runtime-visible local name")
}

func TestValidateAttributes_ClaimReadUndeclaredAliasRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "consumer",
				Executor: "handler.consumer",
				Attributes: &NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"addr": map[string]any{
								"type":   "string",
								"source": "{{claim.ghost.address}}",
							},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.addr.source")
}

func TestValidateFanOut_RejectsUnknownClaim(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				FanOut: &FanOutSpec{
					Claim:            "missing",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out.claim")
}

func TestValidateFanOut_RejectsStoreNotAdvertisingSplitScope(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared:                     storeDeclaredLookup(knownClaimProducers),
		ClaimProducerAdvertisesSplitScope: func(name string) bool { return false },
	})
	require.False(t, res.Ok())
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "supports_split_scope") {
			found = true
			break
		}
	}
	require.True(t, found,
		"validator must reject fan_out template targeting a store not advertising split_scope; errors=%+v", res.Errors)
}

func TestValidateFanOut_AcceptsStoreAdvertisingSplitScope(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared:                     storeDeclaredLookup(knownClaimProducers),
		ClaimProducerAdvertisesSplitScope: func(name string) bool { return true },
	})
	require.True(t, res.Ok(),
		"fan_out template targeting a store that advertises split_scope must register cleanly; errors=%+v", res.Errors)
}

func TestValidateFanOut_RejectsThresholdWithoutMaxFailures(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
					ErrorPolicy: AggregationPolicy{
						Kind: "threshold",
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out.error_policy.max_failures")
}

func TestValidateFanOut_RejectsCarryVerbatimPolicy(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
					ErrorPolicy: AggregationPolicy{
						Kind: "carry_verbatim",
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out.error_policy.kind")
	found := false
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Msg, "carry_verbatim_requires_single_child:") {
			found = true
			require.Contains(t, e.Msg, `"fan"`, "rejection must name the violating node")
		}
	}
	require.True(t, found, "expected carry_verbatim_requires_single_child rejection, got %+v", res.Errors)
}

func TestValidateFanOut_AcceptsDelegateCombo(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Graphs: []GraphSpec{
			{
				Name: MainGraphName,
				Nodes: []TemplateNodeDef{
					{
						Type:     "caller",
						Delegate: "worker",
						ClaimProducers: []NodeClaimProducerRef{
							{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
						},
						FanOut: &FanOutSpec{
							Claim:            "items",
							PartitionRequest: `{"list":[{"key":"a"}]}`,
							ErrorPolicy:      AggregationPolicy{Kind: "strict"},
						},
					},
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []TemplateNodeDef{
					{Type: "inner-entry", Executor: "handler.inner"},
					{Type: "inner-exit", Executor: "handler.inner",
						Subscribes: []SubscriptionEntry{
							{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared:                     storeDeclaredLookup(knownClaimProducers),
		ClaimProducerAdvertisesSplitScope: func(name string) bool { return true },
	})
	assert.True(t, res.Ok(),
		"fan_out composed with delegate on the same node must register cleanly; errors: %+v", res.Errors)
}

func TestValidateFanOut_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: `{"list":[{"key":"a"}]}`,
					Parallelism:      4,
					ErrorPolicy: AggregationPolicy{
						Kind: "strict",
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateClaimProducers_RejectsInvalidLifetime(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Intent: "rw", Selector: "{{params.s}}", Lifetime: "bogus"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].lifetime")
}

func TestValidateClaimProducers_DurableOkAgainstNonDataProcessingProducer(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Intent: "rw", Selector: "{{params.s}}", Lifetime: "durable"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateExecutor_DelegateAndExecutorMutuallyExclusive(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "handler.a",
				Delegate: "subgraph_x",
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].delegate")
}

func TestValidateExecutor_DelegateOk(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "caller", Delegate: "subgraph_x"}}},
			{
				Name:  "subgraph_x",
				Entry: "start",
				Exit:  "done",
				Nodes: []TemplateNodeDef{
					{Type: "start"},
					{Type: "done", Subscribes: []SubscriptionEntry{
						{Node: "start", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
					}},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}
