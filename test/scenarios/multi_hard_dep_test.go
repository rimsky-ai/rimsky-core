// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Multi-hard-dep rendezvous — the two-hard-dep shape.
//
// Receiver `c` declares TWO `hard_dep: true` upstream attribute sources
// (`a` and `b`) whose upstreams settle independently in one frame. The
// suspected livelock: when the later upstream settles, its cascade walk
// reaches `c` and pullHardDepUpstreams re-affirms the EARLIER upstream
// (which already settled this frame and so has no in-flight run row) —
// creating a fresh pending run for it. That re-run settles, walks back
// to `c`, and re-affirms the OTHER settled upstream: mutual re-seeding.
// Each re-seed also inserts a new wait-set blocker on `c`, so the
// receiver never becomes dispatch-eligible and the frame never
// terminates.
//
// Written BEFORE any fix (test-first); kept as the regression pin in
// either outcome.
//
// Phase 1 (instance boot) asserts the boot frames terminate: the
// receiver settles fresh with both upstream values and the dispatch
// counts stabilize. Boot may legally drive the same node in more than
// one serialized frame (every node is a boot source), so phase 1 pins
// termination, not exact counts.
//
// Phase 2 (one invalidation-driven frame — the story's acceptance
// frame) asserts the exact-once rendezvous: invalidating the trigger
// opens ONE frame whose cascade seeds both hard-dep upstreams; each
// upstream runs exactly once, the receiver runs exactly once, after
// both, and the frame terminates within the deadline.
package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestMultiHardDepRendezvous(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("trigger").Success(map[string]any{"t_value": "t-1"}, true, "ok")
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-1"}, true, "ok")
	// @deliberate: Delay b so the two upstreams settle at deterministically distinct
	// instants within the frame: a settles first, b later. The later
	// settler's cascade walk is the one that observes the earlier
	// upstream already settled — the exact re-seeding site under test.
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-1"}, true, "ok").Delay(300 * time.Millisecond)
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "multi-hard-dep-rendezvous", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "trigger", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"t_value": map[string]any{"type": "string"},
					},
					"required": []any{"t_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_value": map[string]any{"type": "string"},
					},
					"required": []any{"a_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b_value": map[string]any{"type": "string"},
					},
					"required": []any{"b_value"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "trigger", Type: "terminal/*"}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a_val": map[string]any{
							"type":     "string",
							"source":   "{{nodes.a.attribute.a_value}}",
							"hard_dep": true,
						},
						"b_val": map[string]any{
							"type":     "string",
							"source":   "{{nodes.b.attribute.b_value}}",
							"hard_dep": true,
						},
					},
					"required": []any{"a_val", "b_val"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-multi-hard-dep", map[string]any{})
	trigN := h.FindNode(iid, "trigger")
	aN := h.FindNode(iid, "a")
	bN := h.FindNode(iid, "b")
	cN := h.FindNode(iid, "c")
	require.NotNil(t, trigN)
	require.NotNil(t, aN)
	require.NotNil(t, bN)
	require.NotNil(t, cN)

	// @constraint: Phase 1 (boot): the boot frames must terminate — a never-
	// fresh c means the walk is livelocked on mutual upstream
	// re-seeding (pre-guard signature: upstream counts in the dozens
	// within the 30s window, receiver count zero).
	frameDone := h.WaitForNodeState(cN.ID, cascade.NodeStateFresh, 30*time.Second)
	require.True(t, frameDone,
		"boot must terminate: c should reach fresh after both hard-dep upstreams settle "+
			"(a never-fresh c means the frame is livelocked on mutual upstream re-seeding); "+
			"per-type dispatch counts at timeout: %v", dispatchCountsByType(h))

	// @constraint: With c settled, the dispatch counts must stop growing — a growing
	// count here is the re-seeding tail.
	bootCounts := awaitStableDispatchCounts(t, h)
	require.GreaterOrEqual(t, bootCounts["c"], 1, "boot: c must have dispatched")

	cRow := latestAttrRowMultiHardDep(t, h, iid, cN.ID)
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-1", cRow.Data["a_val"], "c should see a's first-fire value")
	require.Equal(t, "from-b-1", cRow.Data["b_val"], "c should see b's first-fire value")

	// @deliberate: Phase 2: ONE invalidation-driven frame — the story's
	// acceptance frame. Invalidate the trigger; its cascade affirms c,
	// whose hard-dep pull seeds BOTH upstreams into the same frame;
	// they settle independently (b delayed past a).
	h.Stub.WhenType("trigger").Success(map[string]any{"t_value": "t-2"}, true, "ok")
	h.Stub.WhenType("a").Success(map[string]any{"a_value": "from-a-2"}, true, "ok")
	h.Stub.WhenType("b").Success(map[string]any{"b_value": "from-b-2"}, true, "ok").Delay(300 * time.Millisecond)
	h.Stub.WhenType("c").Success(map[string]any{}, true, "ok")

	adminInvalidateMultiHardDep(t, h, iid, trigN.ID)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cRow = latestAttrRowMultiHardDep(t, h, iid, cN.ID)
		if cRow != nil && cRow.Data["a_val"] == "from-a-2" && cRow.Data["b_val"] == "from-b-2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, cRow)
	require.Equal(t, "from-a-2", cRow.Data["a_val"],
		"frame 2 must terminate with c seeing a's second-fire value")
	require.Equal(t, "from-b-2", cRow.Data["b_val"],
		"frame 2 must terminate with c seeing b's second-fire value")

	// @deliberate: Exact-once rendezvous: relative to the boot baseline, the
	// acceptance frame dispatched each node exactly once. Anything
	// above one is a settled upstream re-affirmed into a fresh run.
	frame2Counts := awaitStableDispatchCounts(t, h)
	for _, typ := range []string{"trigger", "a", "b", "c"} {
		require.Equal(t, bootCounts[typ]+1, frame2Counts[typ],
			"acceptance frame: node type %q must run exactly once — "+
				"a settled hard-dep upstream must not be re-affirmed (mutual re-seeding)", typ)
	}

	// @deliberate: Rendezvous ordering: the receiver's acceptance-frame dispatch
	// (its last) came after both upstreams' acceptance-frame dispatches
	// (their lasts).
	aIdx := lastDispatchIndex(h, "a")
	bIdx := lastDispatchIndex(h, "b")
	cIdx := lastDispatchIndex(h, "c")
	require.Greater(t, cIdx, aIdx, "receiver c must dispatch after upstream a settles")
	require.Greater(t, cIdx, bIdx, "receiver c must dispatch after upstream b settles")
}

// dispatchCountsByType tallies the stub's observed dispatches per node
// type — the livelock signature at timeout is upstream counts far above
// one while the receiver's stays zero/one.
func dispatchCountsByType(h *scenario.Harness) map[string]int {
	got := map[string]int{}
	for _, o := range h.Stub.Observed() {
		got[o.NodeType]++
	}
	return got
}

// awaitStableDispatchCounts waits until the stub's observed dispatch
// count holds still for a full quiesce window, then returns the
// per-type tallies. A count that keeps growing past the deadline is
// the re-seeding tail — fail rather than hang.
func awaitStableDispatchCounts(t *testing.T, h *scenario.Harness) map[string]int {
	t.Helper()
	const quiesce = 2 * time.Second
	deadline := time.Now().Add(20 * time.Second)
	last := len(h.Stub.Observed())
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		cur := len(h.Stub.Observed())
		if cur != last {
			last = cur
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= quiesce {
			return dispatchCountsByType(h)
		}
	}
	require.FailNow(t, "dispatch count never stabilized",
		"dispatches kept arriving past the deadline — mutual re-seeding tail; per-type counts: %v",
		dispatchCountsByType(h))
	return nil
}

// lastDispatchIndex returns the index (in the stub's Execute append
// order) of the last observed dispatch for the given node type, or -1.
func lastDispatchIndex(h *scenario.Harness, typ string) int {
	idx := -1
	for i, o := range h.Stub.Observed() {
		if o.NodeType == typ {
			idx = i
		}
	}
	return idx
}

// @deliberate: latestAttrRow reads the latest main-scope attribute row for a node.
func latestAttrRowMultiHardDep(t *testing.T, h *scenario.Harness, instanceID, nodeID shared.UUID) *persistence.NodeAttributesRow {
	t.Helper()
	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, e := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, nodeID, h.GetMainRunScopeID(instanceID), tx)
		row = r
		return e
	}))
	return row
}

// adminInvalidateMultiHardDep POSTs an admin invalidate against the node.
//
// @source: test/scenarios/per_run_attributes/sequential_runs_test.go:adminInvalidate
func adminInvalidateMultiHardDep(t *testing.T, h *scenario.Harness, instanceID, nodeID interface{ String() string }) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(
		h.ControlBase+"/v1/admin/instances/"+instanceID.String()+"/nodes/"+nodeID.String()+"/invalidate",
		"application/json", bytes.NewReader(body),
	)
	require.NoError(t, err)
	resp.Body.Close()
}
