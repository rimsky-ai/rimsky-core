// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @decision: mode-default-most-recent
func TestCascadeModeDefaultsToMostRecentAndCoalesces(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	releaseB := make(chan struct{})
	h.Stub.WhenType("a").
		Success(map[string]any{"x": "r1"}, true, "a-r1").
		Then().Success(map[string]any{"x": "r2"}, true, "a-r2").
		Then().Success(map[string]any{"x": "r3"}, true, "a-r3")
	h.Stub.WhenType("b").HoldUntil(releaseB).Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "cascade-mode-default", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "test/wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/x/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_x": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.x}}",
						},
					},
					"required": []any{"snapshot_x"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-cascade-mode-default", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	var resolved spec.CascadeMode
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		m, err := h.Persist.Nodes().GetCascadeMode(h.Ctx, b.ID, tx)
		resolved = m
		return err
	}))
	require.Equal(t, spec.CascadeModeMostRecent, resolved,
		"a template omitting cascade_mode must resolve to most-recent")

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	h.PostInstanceMessage(iid, "test/wake", nil, "cascade-mode-default-kick")

	for len(bObs()) < 1 {
		time.Sleep(50 * time.Millisecond)
	}

	driveARound := func(kick string) {
		pauseResp := postJSON(t, h.ControlBase+"/v1/instances/"+iid.String()+"/pause", map[string]any{})
		require.Equal(t, 200, pauseResp.status,
			"pause must succeed while b's dispatch is held in flight (%s); body=%s", kick, string(pauseResp.raw))
		overrideResp := postDebugOverride(t, h.ControlBase, iid, map[string]any{
			"action":    "invalidate_node",
			"node_type": "a",
		}, "")
		require.Equal(t, 200, overrideResp.status,
			"invalidate_node on a must succeed (%s); body=%s", kick, string(overrideResp.raw))
		resumeResp := postJSON(t, h.ControlBase+"/v1/instances/"+iid.String()+"/resume", map[string]any{})
		require.Equal(t, 200, resumeResp.status, "resume must succeed (%s); body=%s", kick, string(resumeResp.raw))
	}

	driveARound("round-2")
	waitForRunCountSettled(h, a.ID, 2)
	driveARound("round-3")
	waitForRunCountSettled(h, a.ID, 3)

	close(releaseB)

	waitForRunCountSettled(h, b.ID, 2)
	h.WaitForAllRunsTerminal(b.ID)

	require.Equal(t, 2, len(bObs()),
		"under the resolved most-recent default, the two cascade rounds a produced while b's "+
			"first dispatch was held in flight must coalesce: the later round deletes the prior "+
			"cascade-driven stale-not-claimed b run at pending→stale, so exactly one post-settle "+
			"dispatch follows the held one — never one dispatch per round")
	require.Equal(t, "r1", bObs()[0].Attributes["snapshot_x"],
		"b's held first dispatch was built from a's first settled value")
	require.Equal(t, "r3", bObs()[1].Attributes["snapshot_x"],
		"the single coalesced post-settle dispatch must see a's latest value")

	var bRunCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{b.ID}, &bRunCount)
	require.Equal(t, 2, bRunCount,
		"b's lineage must show exactly two runs — the coalesced intermediate stale is deleted, "+
			"not left behind as a lineage row")
}
