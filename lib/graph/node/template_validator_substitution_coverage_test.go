// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Validator unit tests for STORY-uncovered-read-rejected
// (spec 2026-06-14-explicit-substitution-cascade-behavior).
//
// As a template author, I get a registration error when a substitution
// ref has no covering subscription, naming the ref and showing the
// subscription entry that would cover it.
//
// Two test functions cover the two uncovered ref shapes:
//   - Per-field attribute ref ({{nodes.X.attribute.Y}}) with no
//     covering subscription.
//   - Whole-pull attribute ref ({{nodes.X.attribute}}) with only a
//     per-field subscription on the same sender (asymmetry rule:
//     decision:coverage-wildcard-asymmetry — the wildcard is required).
//
// A third function pins the post-collapse rejection of the retired
// event substitution form ({{nodes.X.event.Y}}) at directive-grammar
// time — per TD-collapse-named-event-to-tags the event source-kind is
// gone; the directive parser no longer admits it.
//
// Each test asserts the structured `substitution_ref_uncovered` entry
// shape per TD-uncovered-substitution-error-shape: kind discriminator,
// receiver_node_type, ref literal text, attribute_property schema
// path, suggested_subscribes_entry (flat drop-in JSON object with
// four keys), suggested_subscribes_note (sibling, not embedded).
//
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

// TestSubstitutionCoverage_PerFieldAttributeRefUncovered exhibits the
// per-field attribute ref shape: receiver reads `{{nodes.foo.attribute.bar}}`
// with a subscribes: block that names neither `attribute/bar/changed`
// nor `attribute/*` from `foo`. ValidateTemplate must emit a
// structured `substitution_ref_uncovered` entry naming the ref + a
// suggested entry of type `attribute/bar/changed`.
//
//	@story: uncovered-substitution-rejected
func TestSubstitutionCoverage_PerFieldAttributeRefUncovered(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "foo", Executor: "h"},
			{
				Type:     "rcv",
				Executor: "h",
				// @deliberate: no subscribes — the substitution ref below is
				// uncovered.
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

// TestSubstitutionCoverage_WholePullRefUncovered exhibits the
// whole-pull shape: receiver reads `{{nodes.foo.attribute}}` (no field)
// with a subscribes: block that names only the per-field
// `attribute/bar/changed` from `foo`. The asymmetry rule
// (decision:coverage-wildcard-asymmetry) means the per-field does NOT
// cover the whole-pull. The structured entry must suggest the
// wildcard `attribute/*`.
//
//	@story: uncovered-substitution-rejected
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
					// @deliberate: per-field subscription on `bar` does NOT
					// cover the whole-pull below (wildcard-asymmetry rule).
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

// TestSubstitutionCoverage_EventRefRetired pins the post-collapse
// rejection of the retired event substitution form: receiver reads
// `{{nodes.foo.event.something_happened}}` and the directive grammar
// must reject the form at validation time. Per
// TD-collapse-named-event-to-tags the event source-kind is gone; the
// fingerprint surface in the receiver's attribute schema is no longer
// admitted by the directive parser.
//
//	@story: uncovered-substitution-rejected
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
	// @deliberate: the rejection comes from the directive grammar
	// (`nodes.<n>.<second>` admits only `attribute`); look for the
	// grammar's error message anywhere in res.Errors.
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

// findCoverageEntry returns the structured `substitution_ref_uncovered`
// entry from res.StructuredErrors whose receiver matches the given
// type AND whose ref text contains the given substring. Fatals when
// no matching entry is found — the absence is the falsifier.
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

// assertSuggestedEntryShape asserts the structured entry's full
// field set per TD-uncovered-substitution-error-shape:
//   - suggested_subscribes_entry is a flat drop-in JSON object with
//     exactly four keys (node, type, wake_on_change,
//     force_upstream_refresh) and NO embedded _note field
//   - suggested_subscribes_note is a sibling field with the
//     explanatory text containing both flag names
//   - flag defaults in the suggestion are both `false` (the
//     conservative copy-paste shape; the author bumps either to
//     true intentionally per the note)
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
	// be a sibling field, not embedded inside the suggested entry, so
	// the entry remains valid drop-in JSON the author can copy verbatim.
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
