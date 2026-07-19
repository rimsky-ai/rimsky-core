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

func TestValidateHolds_FromNotDependency(t *testing.T) {
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
	hasErrorAt(t, res, "nodes[0].stores[0].lifetime")
}

func TestValidateClaimProducers_DurableRequiresDataProcessing(t *testing.T) {
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
		StoreAdvertisesDataProcessing: func(name string) bool {
			return false
		},
	})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].lifetime")
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
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Delegate: "subgraph_x",
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateClaimProducers_DurableOkWhenDataProcessingAdvertised(t *testing.T) {
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
		StoreAdvertisesDataProcessing: func(name string) bool {
			return name == "content"
		},
	})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}
