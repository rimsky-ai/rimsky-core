// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateHolds_FromNotDependency(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].holds[target].from")
}

func TestValidateHolds_UnknownClaimAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				Stores: []NodeStoreRef{
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].holds[nonexistent]")
}

func TestValidateHolds_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				Stores: []NodeStoreRef{
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// A node that co-holds a claim via holds: may read it through a
// {{claim.<alias>...}}` attribute source — the modern co-holdership
// directive (concept:claim-co-holdership) must support claim reads the
// same way the legacy inherits: form does. Regression for the validator
// omitting holds: aliases from the recognized-alias set.
func TestValidateHolds_ClaimReadFromHeldAliasOk(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "handler.producer",
				Stores: []NodeStoreRef{
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// Reading {{claim.<alias>}}` for an alias that is neither acquired
// (stores:), inherited (inherits:), nor co-held (holds:) is still
// rejected — the holds: fix must not blanket-accept any claim alias.
func TestValidateAttributes_ClaimReadUndeclaredAliasRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.addr.source")
}

func TestValidateFanOut_RejectsUnknownClaim(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				FanOut: &FanOutSpec{
					Claim:            "missing",
					PartitionRequest: "{{trigger.message.payload.x}}",
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out.claim")
}

func TestValidateFanOut_RejectsThresholdWithoutMaxFailures(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				Stores: []NodeStoreRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{trigger.message.payload.x}}",
					ErrorPolicy: AggregationPolicy{
						Kind: "threshold",
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out.error_policy.max_failures")
}

func TestValidateFanOut_RejectsCancelSiblingsOutsideStrict(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				Stores: []NodeStoreRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{trigger.message.payload.x}}",
					ErrorPolicy: AggregationPolicy{
						Kind:           "best_effort",
						CancelSiblings: true,
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out.error_policy.cancel_siblings")
}

// A calling node (`delegate:`) cannot itself declare `fan_out:`. The
// canonicalizer absorbs the sub-graph entry's executor onto the
// calling node, but it does NOT scope fan-out into the absorbed
// sub-graph — every fan-out child would re-fire the internal cascade
// as a separate parent at dispatch. Reject at registration so the
// combination can't reach the runtime.
func TestValidateFanOut_RejectsDelegateCombo(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "caller",
				Delegate: "subgraph_x",
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{trigger.message.payload.x}}",
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].fan_out")
}

func TestValidateFanOut_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "fan",
				Executor: "handler.fan",
				Stores: []NodeStoreRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "{{params.s}}"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{trigger.message.payload.x}}",
					Parallelism:      4,
					ErrorPolicy: AggregationPolicy{
						Kind:           "strict",
						CancelSiblings: true,
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_RejectsInvalidLifetime(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "content", Intent: "rw", Selector: "{{params.s}}", Lifetime: "bogus"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].lifetime")
}

func TestValidateStores_DurableRequiresDataProcessing(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "content", Intent: "rw", Selector: "{{params.s}}", Lifetime: "durable"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		StoreAdvertisesDataProcessing: func(name string) bool {
			return false
		},
	})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].lifetime")
}

func TestValidateExecutor_DelegateAndExecutorMutuallyExclusive(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "handler.a",
				Delegate: "subgraph_x",
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].delegate")
}

func TestValidateExecutor_DelegateOk(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Delegate: "subgraph_x",
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	// A delegate-only node (no executor) is currently legal at the
	// validator level — the canonicalizer absorbs the entry's
	// executor at registration. The validator should not block this.
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_DurableOkWhenDataProcessingAdvertised(t *testing.T) {
	spec := &TemplateSpec{
		Name:                "demo",
		Version:             "1.0.0",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type:     "a",
				Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "content", Intent: "rw", Selector: "{{params.s}}", Lifetime: "durable"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
		StoreAdvertisesDataProcessing: func(name string) bool {
			return name == "content"
		},
	})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}
