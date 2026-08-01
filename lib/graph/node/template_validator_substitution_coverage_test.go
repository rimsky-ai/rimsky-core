// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

//	@story: uncovered-substitution-rejected
//	@decision: substitution-ref-coverage-required
//	@decision: coverage-wildcard-asymmetry
//	@decision: uncovered-substitution-error-shape

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// @story: uncovered-substitution-rejected
func TestSubstitutionCoverage_PerFieldAttributeRefUncovered(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "foo", Executor: "h"},
			{
				Type:     "rcv",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"copied": map[string]any{
							"type":   "string",
							"source": "{{nodes.foo.attribute.bar}}",
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(), "validator must reject the uncovered ref")
	entry := findCoverageEntry(t, res, "rcv", "nodes.foo.attribute.bar")
	assertSuggestedEntryShape(t, entry, "foo", "attribute/bar/changed")
	require.Contains(t, entry["attribute_property"], "copied",
		"attribute_property must name the schema path the ref appeared in")
}

// @story: uncovered-substitution-rejected
func TestSubstitutionCoverage_WholePullRefUncovered(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "foo", Executor: "h"},
			{
				Type:     "rcv",
				Executor: "h",
				Subscribes: []SubscriptionEntry{
					{
						Node:                 "foo",
						Type:                 "attribute/bar/changed",
						ForceUpstreamRefresh: BoolPtr(false),
					},
				},
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"all_of_foo": map[string]any{
							"type":   "object",
							"source": "{{nodes.foo.attribute}}",
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(), "validator must reject the uncovered whole-pull "+
		"despite the per-field subscription (decision:coverage-wildcard-asymmetry)")
	entry := findCoverageEntry(t, res, "rcv", "nodes.foo.attribute")
	assertSuggestedEntryShape(t, entry, "foo", "attribute/*")
	require.Contains(t, entry["attribute_property"], "all_of_foo",
		"attribute_property must name the schema path the whole-pull appeared in")
}

// @story: uncovered-substitution-rejected
func TestSubstitutionCoverage_EventRefRetired(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "foo", Executor: "h"},
			{
				Type:     "rcv",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"latest_event": map[string]any{
							"type":   "object",
							"source": "{{nodes.foo.event.something_happened}}",
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(), "validator must reject the retired event substitution form")
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "event") || strings.Contains(e.Msg, "second segment must be 'attribute'") {
			found = true
			break
		}
	}
	require.True(t, found,
		"validator must surface a grammar error for the retired event form; got: %+v", res.Errors)
}

func findCoverageEntry(t *testing.T, res ValidationResult, receiver, refContains string) map[string]any {
	t.Helper()
	for _, e := range res.StructuredErrors {
		kind, _ := e["kind"].(string)
		if kind != "substitution_ref_uncovered" {
			continue
		}
		rcv, _ := e["receiver_node_type"].(string)
		if rcv != receiver {
			continue
		}
		ref, _ := e["ref"].(string)
		if strings.Contains(ref, refContains) {
			return e
		}
	}
	t.Fatalf("no structured substitution_ref_uncovered entry for receiver=%q "+
		"with ref containing %q; got: %+v", receiver, refContains, res.StructuredErrors)
	return nil
}

func assertSuggestedEntryShape(t *testing.T, entry map[string]any, expectedSender, expectedType string) {
	t.Helper()

	suggested, ok := entry["suggested_subscribes_entry"].(map[string]any)
	require.True(t, ok, "suggested_subscribes_entry must be a JSON object: %+v", entry)
	require.Len(t, suggested, 3,
		"suggested_subscribes_entry must be a flat 3-key drop-in JSON object "+
			"(no embedded _note field per decision:uncovered-substitution-error-shape): %+v",
		suggested)
	require.Equal(t, expectedSender, suggested["node"],
		"suggested_subscribes_entry.node must name the sender")
	require.Equal(t, expectedType, suggested["type"],
		"suggested_subscribes_entry.type must match the implied signal type")
	require.Equal(t, false, suggested["force_upstream_refresh"],
		"the suggested entry's force_upstream_refresh default must be false (conservative)")

	// @decision: uncovered-substitution-error-shape
	_, hasNoteInside := suggested["_note"]
	require.False(t, hasNoteInside,
		"suggested_subscribes_entry must not embed a _note field "+
			"(decision:uncovered-substitution-error-shape — keep the entry drop-in)")
	note, ok := entry["suggested_subscribes_note"].(string)
	require.True(t, ok, "suggested_subscribes_note must be a top-level string sibling: %+v", entry)
	require.NotEmpty(t, note)
	require.Contains(t, note, "force_upstream_refresh",
		"the note must mention force_upstream_refresh so the author understands the flag's effect")
}

// @story: typed-message-substitution
// @decision: substitution-ref-coverage-required
func TestCoverageCheck_MessagesUndeclaredRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "fanout", Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "@x"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{messages.ev/bar.X}}",
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(), "template with `{{messages.ev/bar.X}}` and no `ev/bar` in messages: must reject")
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "ev/bar") && (strings.Contains(e.Msg, "messages:") || strings.Contains(e.Msg, "messages registry")) {
			found = true
			break
		}
	}
	require.True(t, found,
		"validator must surface a clear error naming `ev/bar` and the missing messages registry declaration; got errors=%+v structured=%+v", res.Errors, res.StructuredErrors)
}

// @story: typed-message-substitution
// @decision: substitution-ref-coverage-required
func TestCoverageCheck_MessagesDeclaredAccepted(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ev/bar", BodySchema: []byte(`{"type":"object","properties":{"X":{"type":"array"}}}`)},
		},
		Nodes: []TemplateNodeDef{
			{Type: "fanout", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{
						Node:                 "ev/bar",
						Type:                 "terminal/success",
						ForceUpstreamRefresh: BoolPtr(false),
					},
				},
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "@x"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{messages.ev/bar.X}}",
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.True(t, res.Ok(), "declared messages.bar + subscribed must register cleanly; errors=%+v structured=%+v", res.Errors, res.StructuredErrors)
}

// @story: typed-message-substitution
// @decision: substitution-ref-coverage-required
func TestCoverageCheck_SymmetryWithNodes(t *testing.T) {
	t.Run("nodes.foo path", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "foo", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"items": map[string]any{"type": "array"},
						},
					}},
				},
				{Type: "rcv", Executor: "h",
					Subscribes: []SubscriptionEntry{
						{
							Node:                 "foo",
							Type:                 "attribute/items/changed",
							ForceUpstreamRefresh: BoolPtr(false),
						},
					},
					ClaimProducers: []NodeClaimProducerRef{
						{Name: "content", Alias: "data", Intent: "r", Selector: "@x"},
					},
					FanOut: &FanOutSpec{
						Claim:            "data",
						PartitionRequest: "{{nodes.foo.attribute.items}}",
						ErrorPolicy:      AggregationPolicy{Kind: "strict"},
					},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		})
		require.True(t, res.Ok(), "nodes.foo path errors=%+v structured=%+v", res.Errors, res.StructuredErrors)
	})

	t.Run("messages.foo path", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Messages: []MessageSchema{
				{Type: "ev/foo", BodySchema: []byte(`{"type":"object","properties":{"items":{"type":"array"}}}`)},
			},
			Nodes: []TemplateNodeDef{
				{Type: "rcv", Executor: "h",
					Subscribes: []SubscriptionEntry{
						{
							Node:                 "ev/foo",
							Type:                 "terminal/success",
							ForceUpstreamRefresh: BoolPtr(false),
						},
					},
					ClaimProducers: []NodeClaimProducerRef{
						{Name: "content", Alias: "data", Intent: "r", Selector: "@x"},
					},
					FanOut: &FanOutSpec{
						Claim:            "data",
						PartitionRequest: "{{messages.ev/foo.items}}",
						ErrorPolicy:      AggregationPolicy{Kind: "strict"},
					},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		})
		require.True(t, res.Ok(), "messages.foo path errors=%+v structured=%+v", res.Errors, res.StructuredErrors)
	})
}

// @story: typed-message-substitution
// @decision: substitution-ref-coverage-required
// @story: typed-message-substitution
// @decision: substitution-ref-coverage-required
func TestCoverageCheck_MessagesDeclaredButUncoveredRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ev/bar", BodySchema: []byte(`{"type":"object","properties":{"X":{"type":"array"}}}`)},
		},
		Nodes: []TemplateNodeDef{
			{Type: "fanout", Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "items", Intent: "r", Selector: "@x"},
				},
				FanOut: &FanOutSpec{
					Claim:            "items",
					PartitionRequest: "{{messages.ev/bar.X}}",
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(),
		"a message type declared in messages: but with no covering subscription must be rejected as uncovered")
	entry := findCoverageEntry(t, res, "fanout", "messages.ev/bar.X")
	assertSuggestedEntryShape(t, entry, "ev/bar", "terminal/success")
	require.Equal(t, "fan_out.partition_request", entry["attribute_property"],
		"attribute_property must name the ref's actual position (fan_out.partition_request), not be hardcoded empty")
}

func TestCoverageCheck_MessagesWildcardAccepted(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ev/foo", BodySchema: []byte(`{"type":"object","properties":{"items":{"type":"array"}}}`)},
		},
		Nodes: []TemplateNodeDef{
			{Type: "rcv", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{
						Node:                 "ev/foo",
						Type:                 "terminal/*",
						ForceUpstreamRefresh: BoolPtr(false),
					},
				},
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "data", Intent: "r", Selector: "@x"},
				},
				FanOut: &FanOutSpec{
					Claim:            "data",
					PartitionRequest: "{{messages.ev/foo.items}}",
					ErrorPolicy:      AggregationPolicy{Kind: "strict"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.True(t, res.Ok(), "terminal/* wildcard subscription must cover messages.<type> refs symmetrically with attribute/* covering nodes.<type>; errors=%+v structured=%+v", res.Errors, res.StructuredErrors)
}
