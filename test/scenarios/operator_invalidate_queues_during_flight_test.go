// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: operator-invalidate-queues-during-flight
func TestOperatorInvalidateQueuesDuringFlight(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC()
	clock := shared.NewControllableClock(base)
	h := scenario.Start(t, scenario.HarnessOpts{Clock: clock})

	resumeAt := base.Add(10 * time.Second)
	h.Stub.WhenType("worker").Park(resumeAt)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "operator-invalidate-queues-during-flight", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-operator-invalidate-during-flight", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.PostInstanceMessage(iid, "test/wake", nil, "test-wake-1")

	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)
	require.Equal(t, 1, stubWorkerCount(h), "worker invoked exactly once before invalidate")

	pauseResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/pause", iid.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, pauseResp.status,
		"pause must succeed so the operator surface is debuggable; body=%s", string(pauseResp.raw))

	overrideResp := postDebugOverride(t, h.ControlBase, iid, map[string]any{
		"action":          "set_attribute",
		"node_type":       "worker",
		"attribute_key":   "prior_marker",
		"attribute_value": "from-parked-run",
	}, "")
	require.Equal(t, http.StatusOK, overrideResp.status,
		"operator invalidate via the debug-override route must be accepted; body=%s",
		string(overrideResp.raw))
	var applied struct {
		OK          bool   `json:"ok"`
		GateState   string `json:"gate_state"`
		RunsMutated int    `json:"runs_mutated"`
	}
	require.NoError(t, json.Unmarshal(overrideResp.raw, &applied))
	require.True(t, applied.OK)
	require.Equal(t, "paused", applied.GateState,
		"the override gate must report `paused` for the paused-instance path")
	require.GreaterOrEqual(t, applied.RunsMutated, 1,
		"the override must mutate at least one run — a 200 with runs_mutated=0 means the "+
			"handler returned a status struct without driving the invalidate through the graph")

	var staleCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND creation_reason = 'operator_invalidate' AND state = 'stale'`,
		[]any{worker.ID}, &staleCount,
	)
	require.Equal(t, 1, staleCount,
		"the operator-invalidate route must create exactly one stale row with creation_reason=operator_invalidate")

	var newRunID string
	h.QueryRowSQL(
		`SELECT id::text FROM rimsky_node_runs WHERE node_id = $1 AND creation_reason = 'operator_invalidate'`,
		[]any{worker.ID}, &newRunID,
	)

	var (
		carriedData string
		dispatchBag sql.NullString
	)
	h.QueryRowSQL(
		`SELECT data::text, dispatch_input_bag::text FROM rimsky_node_attributes WHERE node_run_id = $1`,
		[]any{newRunID}, &carriedData, &dispatchBag,
	)
	require.Contains(t, carriedData, "prior_marker",
		"operator-invalidate stale must carry forward the live bag the route merged into the in-flight run")
	require.True(t, dispatchBag.Valid && dispatchBag.String != "",
		"operator-invalidate stale must snapshot a dispatch_input_bag at row creation per the non-cascade-direct-to-stale decision")
	require.Contains(t, dispatchBag.String, "prior_marker",
		"snapshot dispatch_input_bag must mirror the carried-forward live bag")

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "worker-resumed")

	resumeResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iid.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeResp.status,
		"resume must succeed so dispatch can flow again; body=%s", string(resumeResp.raw))

	stableCounts := awaitStableDispatchCounts(t, h)
	require.Equal(t, 1, stableCounts["worker"],
		"with the instance unpaused and the clock still frozen before resume_at, the operator-invalidate "+
			"stale must NOT dispatch while the parked predecessor is in-flight; the dispatcher's serialization "+
			"gate — not the pause — blocks it. Checked after a quiesce window (not a single unwaited "+
			"snapshot) so a slower-polling broken gate is still caught.")

	clock.Advance(15 * time.Second)

	h.WaitForEventKind(worker.ID, "parked_resume_started")

	waitForStubWorkerCount(h, 3)
	require.Equal(t, 3, stubWorkerCount(h),
		"worker must be invoked exactly three times, no more")

	var creationReasons string
	h.QueryRowSQL(
		`SELECT string_agg(creation_reason, ',' ORDER BY sequence) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{worker.ID}, &creationReasons,
	)
	require.Equal(t, "cascade,operator_invalidate", creationReasons,
		"worker's lineage must show one cascade-driven run (the parked-then-resumed one) "+
			"followed by one operator_invalidate run")
}
