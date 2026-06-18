// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		StoreDeclared: storeDeclaredLookup(knownStores),
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
						WakeOnChange:         BoolPtr(true),
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
		StoreDeclared: storeDeclaredLookup(knownStores),
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
		StoreDeclared: storeDeclaredLookup(knownStores),
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
	require.Len(t, suggested, 4,
		"suggested_subscribes_entry must be a flat 4-key drop-in JSON object "+
			"(no embedded _note field per decision:uncovered-substitution-error-shape): %+v",
		suggested)
	require.Equal(t, expectedSender, suggested["node"],
		"suggested_subscribes_entry.node must name the sender")
	require.Equal(t, expectedType, suggested["type"],
		"suggested_subscribes_entry.type must match the implied signal type")
	require.Equal(t, false, suggested["wake_on_change"],
		"the suggested entry's wake_on_change default must be false (conservative)")
	require.Equal(t, false, suggested["force_upstream_refresh"],
		"the suggested entry's force_upstream_refresh default must be false (conservative)")

	// @decision: uncovered-substitution-error-shape — the note must
	_, hasNoteInside := suggested["_note"]
	require.False(t, hasNoteInside,
		"suggested_subscribes_entry must not embed a _note field "+
			"(decision:uncovered-substitution-error-shape — keep the entry drop-in)")
	note, ok := entry["suggested_subscribes_note"].(string)
	require.True(t, ok, "suggested_subscribes_note must be a top-level string sibling: %+v", entry)
	require.NotEmpty(t, note)
	require.Contains(t, note, "wake_on_change",
		"the note must mention wake_on_change so the author understands the flag's effect")
	require.Contains(t, note, "force_upstream_refresh",
		"the note must mention force_upstream_refresh so the author understands the flag's effect")
}
