// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: idempotent-mode-dedupes
func TestIdempotentMode_SubstitutionFailureRoutesBeforeModeRule(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-should-not-run-round2")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "idempotent-substitution-failure-ordering", Version: "1",
		Messages: []spec.MessageSchema{
			{
				Type: "test/detail-wake",
				BodySchema: spec.RawJSON(`{
					"type": "object",
					"properties": {"reason": {"type": "string"}}
				}`),
			},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:        "b",
					Executor:    "stub",
					CascadeMode: spec.CascadeModeIdempotentQueue,
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"template_resolution_failed": {Action: "give_up"},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "test/detail-wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_reason": map[string]any{
							"type":   "string",
							"source": "{{messages.test/detail-wake.reason}}",
						},
					},
					"required": []any{"snapshot_reason"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-idempotent-substitution-failure-ordering", map[string]any{})
	b := h.FindNode(iid, "b")
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/detail-wake", []byte(`{"reason":"ok"}`), "idempotent-substitution-kick-1")

	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)

	h.PostInstanceMessage(iid, "test/detail-wake", []byte(`{}`), "idempotent-substitution-kick-2")

	waitForSettlingSignalTypePrefix(t, h, b.ID, "terminal/error/")

	var errorRunCount int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_node_runs
		 WHERE node_id = $1 AND settling_signal_type = 'terminal/error/template_resolution_failed'`,
		[]any{b.ID}, &errorRunCount)
	require.Equal(t, 1, errorRunCount,
		"under cascade_mode=idempotent-queue, a cascade round whose resolved bag cannot even be built "+
			"(the second test/detail-wake delivery carries no 'reason' field, so {{messages.test/detail-wake.reason}} "+
			"has no source) must route through error policy (give_up here) rather than being silently "+
			"dropped by applyCascadeModeRule as if it were just another duplicate input bag: "+
			"routeSubstitutionFailureAtGate in evaluateOneGate must run BEFORE GetCascadeMode / "+
			"applyCascadeModeRule is ever consulted, not after")

	var totalRunCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{b.ID}, &totalRunCount)
	require.Equal(t, 2, totalRunCount,
		"exactly two b runs must exist: the round-1 success and the round-2 error-routed run; a silent "+
			"mode-drop would either leave the round-2 pending run stuck unresolved or delete it without "+
			"ever recording an error, not surface as a second, properly-terminated run")

	require.True(t, h.HasEventKind(b.ID, "template_resolution_failed"),
		"the substitution failure must be recorded as a template_resolution_failed event, proving it "+
			"was classified and routed through the error-policy path rather than vanishing as a mode-drop")
}
