// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable proof for STORY-uncovered-read-rejected — control-API
// boundary half (spec 2026-06-14-explicit-substitution-cascade-behavior).
//
// The validator unit tests in
// lib/graph/node/template_validator_substitution_coverage_test.go
// pin the structured-entry shape in-process. This scenario test boots
// the real assembled stack via testcontainers and submits each of the
// three uncovered-ref templates through the actual POST /v1/templates
// HTTP boundary the operator interacts with — so the proof exercises
// the response-rendering site that builds the JSON the operator sees
// (lib/control/controlapi/templates.go), not just the validator that
// produces the structured entries.
//
// Per TD-uncovered-substitution-error-shape: the rationale for the
// structured shape is programmatic fix-suggestion delivered through
// the operator's actual surface — keep the proof at that surface.
//
// Each sub-test asserts HTTP 400; that the response body carries a
// `validation_errors` array containing exactly one entry with
// `kind: "substitution_ref_uncovered"` for the test's receiver; that
// the entry's six fields match the expected payload (kind,
// receiver_node_type, ref, attribute_property, suggested_subscribes_entry,
// suggested_subscribes_note); that the suggested_subscribes_entry is a
// flat drop-in JSON object with exactly four keys; and that
// suggested_subscribes_note is non-empty and mentions both flag names.
//
//	@story: uncovered-substitution-rejected
package scenarios

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestRegistrationRejectsUncoveredSubstitution submits each uncovered
// substitution-ref template to POST /v1/templates and asserts the
// rejection response carries the structured `substitution_ref_uncovered`
// entry the operator (human or LLM) can act on programmatically.
//
//	@story: uncovered-substitution-rejected
func TestRegistrationRejectsUncoveredSubstitution(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	cases := []struct {
		name             string
		spec             map[string]any
		receiver         string
		refContains      string
		attrPropContains string
		suggestedSender  string
		suggestedType    string
	}{
		{
			name:             "per_field_attribute_ref",
			spec:             perFieldUncoveredSpec("uncovered-perfield-attr", "1"),
			receiver:         "rcv",
			refContains:      "nodes.foo.attribute.bar",
			attrPropContains: "copied",
			suggestedSender:  "foo",
			suggestedType:    "attribute/bar/changed",
		},
		{
			name:             "whole_pull_ref_with_per_field_subscription",
			spec:             wholePullUncoveredSpec("uncovered-wholepull-attr", "1"),
			receiver:         "rcv",
			refContains:      "nodes.foo.attribute",
			attrPropContains: "all_of_foo",
			suggestedSender:  "foo",
			// asymmetry rule: per-field subscription does not cover the
			// whole-pull; the wildcard is required.
			suggestedType: "attribute/*",
		},
		{
			name:             "event_ref",
			spec:             eventUncoveredSpec("uncovered-event-ref", "1"),
			receiver:         "rcv",
			refContains:      "nodes.foo.event.something_happened",
			attrPropContains: "latest_event",
			suggestedSender:  "foo",
			suggestedType:    "event/something_happened",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, h.ControlBase+"/v1/templates", map[string]any{
				"spec": tc.spec,
			})
			require.Equal(t, http.StatusBadRequest, resp.status,
				"registration must reject the uncovered ref with HTTP 400: %s",
				resp.bodyStr())

			errsAny, ok := resp.body["validation_errors"].([]any)
			require.True(t, ok,
				"response body must carry a validation_errors array: %s",
				resp.bodyStr())

			entry := findRegistrationCoverageEntry(t, errsAny, tc.receiver, tc.refContains)

			require.Equal(t, "substitution_ref_uncovered", entry["kind"],
				"kind discriminator must mark the structured shape")
			require.Equal(t, tc.receiver, entry["receiver_node_type"],
				"receiver_node_type must name the receiver carrying the uncovered ref")
			refStr, _ := entry["ref"].(string)
			require.Contains(t, refStr, tc.refContains,
				"ref must carry the literal directive text")
			attrProp, _ := entry["attribute_property"].(string)
			require.Contains(t, attrProp, tc.attrPropContains,
				"attribute_property must name the schema path the ref appears in")

			suggested, ok := entry["suggested_subscribes_entry"].(map[string]any)
			require.True(t, ok,
				"suggested_subscribes_entry must be a JSON object: %+v", entry)
			require.Len(t, suggested, 4,
				"suggested_subscribes_entry must be a flat 4-key drop-in JSON "+
					"object (node, type, wake_on_change, force_upstream_refresh) "+
					"per decision:uncovered-substitution-error-shape: %+v", suggested)
			require.Equal(t, tc.suggestedSender, suggested["node"],
				"suggested_subscribes_entry.node must name the sender")
			require.Equal(t, tc.suggestedType, suggested["type"],
				"suggested_subscribes_entry.type must match the implied signal type")
			require.Equal(t, false, suggested["wake_on_change"],
				"suggested wake_on_change default must be false (conservative)")
			require.Equal(t, false, suggested["force_upstream_refresh"],
				"suggested force_upstream_refresh default must be false (conservative)")

			note, ok := entry["suggested_subscribes_note"].(string)
			require.True(t, ok,
				"suggested_subscribes_note must be a sibling string field: %+v", entry)
			require.NotEmpty(t, note,
				"suggested_subscribes_note must be non-empty")
			require.Contains(t, note, "wake_on_change",
				"the note must mention wake_on_change so the author understands the flag effect")
			require.Contains(t, note, "force_upstream_refresh",
				"the note must mention force_upstream_refresh so the author understands the flag effect")
		})
	}
}

// perFieldUncoveredSpec builds the inner `spec:` map for a template
// whose receiver reads {{nodes.foo.attribute.bar}} via per-field
// source: directive but declares no covering subscription.
func perFieldUncoveredSpec(name, version string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               version,
		"nodes": []map[string]any{
			{"type": "foo", "executor": "stub"},
			{
				"type":     "rcv",
				"executor": "stub",
				// NO subscribes — the substitution ref is uncovered.
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"copied": map[string]any{
								"type":   "string",
								"source": "{{nodes.foo.attribute.bar}}",
							},
						},
					},
				},
			},
		},
	}
}

// wholePullUncoveredSpec builds the inner `spec:` map for a template
// whose receiver reads {{nodes.foo.attribute}} (whole-pull) with only
// a per-field attribute/bar/changed subscription on foo. The
// asymmetry rule rejects this: the wildcard attribute/* is required.
func wholePullUncoveredSpec(name, version string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               version,
		"nodes": []map[string]any{
			{"type": "foo", "executor": "stub"},
			{
				"type":     "rcv",
				"executor": "stub",
				"subscribes": []map[string]any{
					{
						"node":                   "foo",
						"type":                   "attribute/bar/changed",
						"wake_on_change":         true,
						"force_upstream_refresh": false,
					},
				},
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"all_of_foo": map[string]any{
								"type":   "object",
								"source": "{{nodes.foo.attribute}}",
							},
						},
					},
				},
			},
		},
	}
}

// eventUncoveredSpec builds the inner `spec:` map for a template whose
// receiver reads {{nodes.foo.event.something_happened}} but declares
// no covering event/something_happened subscription on foo.
func eventUncoveredSpec(name, version string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               version,
		"nodes": []map[string]any{
			{"type": "foo", "executor": "stub"},
			{
				"type":     "rcv",
				"executor": "stub",
				// NO subscribes — the event ref is uncovered.
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"latest_event": map[string]any{
								"type":   "object",
								"source": "{{nodes.foo.event.something_happened}}",
							},
						},
					},
				},
			},
		},
	}
}

// findRegistrationCoverageEntry scans the validation_errors array for
// the structured substitution_ref_uncovered entry matching the given
// receiver and ref substring, asserts exactly one such entry is
// present, and returns it. Fatals when zero or two-or-more entries
// match — both are falsifier conditions.
func findRegistrationCoverageEntry(t *testing.T, errs []any, receiver, refContains string) map[string]any {
	t.Helper()
	matches := []map[string]any{}
	for _, e := range errs {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := m["kind"].(string)
		if kind != "substitution_ref_uncovered" {
			continue
		}
		rcv, _ := m["receiver_node_type"].(string)
		if rcv != receiver {
			continue
		}
		ref, _ := m["ref"].(string)
		if strings.Contains(ref, refContains) {
			matches = append(matches, m)
		}
	}
	require.Len(t, matches, 1,
		"expected exactly one substitution_ref_uncovered entry for receiver=%q ref containing %q, got %d: %+v",
		receiver, refContains, len(matches), errs)
	return matches[0]
}
