// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//	@story: uncovered-substitution-rejected
package scenarios

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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
			suggestedType: "attribute/*",
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

	t.Run("event_ref_retired", func(t *testing.T) {
		resp := postJSON(t, h.ControlBase+"/v1/templates", map[string]any{
			"spec": eventUncoveredSpec("uncovered-event-ref", "1"),
		})
		require.Equal(t, http.StatusBadRequest, resp.status,
			"registration must reject the retired event substitution form with HTTP 400: %s",
			resp.bodyStr())
		errsAny, ok := resp.body["validation_errors"].([]any)
		require.True(t, ok,
			"response body must carry a validation_errors array: %s", resp.bodyStr())
		var found bool
		for _, e := range errsAny {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			msg, _ := m["msg"].(string)
			if strings.Contains(msg, "event") || strings.Contains(msg, "second segment must be 'attribute'") {
				found = true
				break
			}
		}
		require.True(t, found,
			"validation_errors must mention the retired event form: %+v", errsAny)
	})
}

func perFieldUncoveredSpec(name, version string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": version,
		"nodes": []map[string]any{
			{"type": "foo", "executor": "stub"},
			{
				"type":     "rcv",
				"executor": "stub",
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

func wholePullUncoveredSpec(name, version string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": version,
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

func eventUncoveredSpec(name, version string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": version,
		"nodes": []map[string]any{
			{"type": "foo", "executor": "stub"},
			{
				"type":     "rcv",
				"executor": "stub",
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
