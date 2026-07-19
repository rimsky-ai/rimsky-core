// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: idempotent-mode-dedupes
func TestIdempotentModeDedupes_QueueComparison(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		Success(map[string]any{"counter": 1, "stable": "same"}, true, "a-r1").
		Then().Success(map[string]any{"counter": 2, "stable": "same"}, true, "a-r2").
		Then().Success(map[string]any{"counter": 3, "stable": "same"}, true, "a-r3").
		Then().Success(map[string]any{"counter": 4, "stable": "same"}, true, "a-r4")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "idempotent-queue-dedupes", Version: "1",
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
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/counter/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"counter": map[string]any{"type": "integer", "readOnly": true},
						"stable":  map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"counter", "stable"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:        "b",
					Executor:    "stub",
					CascadeMode: string(cascade.CascadeModeIdempotentQueue),
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/stable/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_stable": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.stable}}",
						},
					},
					"required": []any{"snapshot_stable"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-idempotent-queue", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	h.PostInstanceMessage(iid, "test/wake", nil, "idempotent-kick")

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	for len(bObs()) < 1 {
		time.Sleep(50 * time.Millisecond)
	}
	waitForRunCountSettled(h, a.ID, 5)
	h.WaitForAllRunsTerminal(b.ID)

	require.Equal(t, 1, len(bObs()),
		"under cascade_mode=idempotent-queue, a's four scripted cascade rounds (plus a fifth "+
			"steady-state repeat of the last script, which self-loops once more but produces "+
			"no further attribute change) all produce a b input bag of "+
			"{snapshot_stable: \"same\"} (byte-identical). The gate evaluator's "+
			"modeDropIfPriorEqual JCS-compares each new pending's resolved bag against the "+
			"prior cascade stale's bag; when equal, the new pending is DROPPED. Only the "+
			"first cascade survives to a dispatch; the rest dedup at pending→stale. b must "+
			"be invoked exactly once.")

	require.Equal(t, "same", bObs()[0].Attributes["snapshot_stable"],
		"the single b dispatch must see a's stable value")
}

// @story: idempotent-mode-dedupes
func TestIdempotentModeDedupes_SettledComparison(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	keeperHold := make(chan struct{})
	h.Stub.WhenType("keeper").Success(map[string]any{}, true, "keeper").HoldUntil(keeperHold)
	h.Stub.WhenType("a").Success(map[string]any{"stable": "same"}, true, "a-run")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "idempotent-settled-dedupes", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "keeper",
				Executor: "stub",
				Subscribes: []node.SubscriptionEntry{
					{
						Node: "test/wake", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				},
			},
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "a",
					Executor: "stub",
					Subscribes: []node.SubscriptionEntry{
						{
							Node: "test/wake", Type: "terminal/success",
							ForceUpstreamRefresh: node.BoolPtr(false),
						},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"stable": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"stable"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:        "b",
					Executor:    "stub",
					CascadeMode: string(cascade.CascadeModeIdempotentSettled),
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{
						Node: "a", Type: "terminal/success",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
					node.SubscriptionEntry{
						Node: "a", Type: "attribute/stable/changed",
						ForceUpstreamRefresh: node.BoolPtr(false),
					},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshot_stable": map[string]any{
							"type":   "string",
							"source": "{{nodes.a.attribute.stable}}",
						},
					},
					"required": []any{"snapshot_stable"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-idempotent-settled", map[string]any{})
	keeper := h.FindNode(iid, "keeper")
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, keeper)
	require.NotNil(t, a)
	require.NotNil(t, b)

	bObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "b" {
				out = append(out, o)
			}
		}
		return out
	}

	h.PostInstanceMessage(iid, "test/wake", nil, "settled-kick")

	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)

	require.Equal(t, 1, len(bObs()),
		"a's first (and, absent a second trigger, only) cascade round must dispatch and "+
			"settle b before this test manually retriggers a; keeper's held dispatch keeps "+
			"the frame running throughout so the manual retrigger below has a frame to land in")

	pauseResp := postJSON(t, h.ControlBase+"/v1/instances/"+iid.String()+"/pause", map[string]any{})
	require.Equal(t, 200, pauseResp.status, "pause must succeed while keeper's dispatch is still in flight; body=%s", string(pauseResp.raw))

	overrideResp := postDebugOverride(t, h.ControlBase, iid, map[string]any{
		"action":    "invalidate_node",
		"node_type": "a",
	}, "")
	require.Equal(t, 200, overrideResp.status, "invalidate_node on a must succeed; body=%s", string(overrideResp.raw))

	resumeResp := postJSON(t, h.ControlBase+"/v1/instances/"+iid.String()+"/resume", map[string]any{})
	require.Equal(t, 200, resumeResp.status, "resume must succeed; body=%s", string(resumeResp.raw))

	waitForRunCountSettled(h, a.ID, 2)

	close(keeperHold)
	h.WaitForNodeState(keeper.ID, cascade.NodeStateFresh)
	h.WaitForAllRunsTerminal(b.ID)

	require.Equal(t, 1, len(bObs()),
		"under cascade_mode=idempotent-settled, a's manually retriggered second run (a fresh "+
			"terminal/success, a genuine cascade-reason signal to b, even though a itself was "+
			"retriggered via operator_invalidate) produces the same b input bag of "+
			"{snapshot_stable: \"same\"} as the first. b's round-1 run has already settled "+
			"(fresh, and a has zero in-flight runs at this point, so there is no "+
			"cascade-driven stale-not-claimed predecessor), so modeDropIfPriorEqual falls "+
			"back to GetMostRecentSettledRun and JCS-compares the new pending's resolved bag "+
			"against it; when equal, the new pending is DROPPED even though the comparison "+
			"window crossed a settle boundary. b must still have been invoked exactly once.")

	var bRunCount int
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{b.ID}, &bRunCount)
	require.Equal(t, 1, bRunCount,
		"b's lineage must show exactly one run: the dropped second pending is deleted "+
			"by DropPendingRun, not left behind as a second lineage row")
}

func waitForRunCountSettled(h *scenario.Harness, nodeID shared.UUID, want int) {
	for {
		var total, inflight int
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`,
			[]any{nodeID}, &total)
		h.QueryRowSQL(`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1
			AND state IN ('pending','stale','running','held','parked')`,
			[]any{nodeID}, &inflight)
		if total == want && inflight == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
