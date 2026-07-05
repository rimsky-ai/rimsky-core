// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: operator-invalidate-queues-during-flight
func TestOperatorInvalidateQueuesDuringFlight(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	resumeAt := time.Now().Add(8 * time.Second)
	h.Stub.WhenType("worker").Park(genv1.ParkReason_PARK_REASON_SNOOZE, "snooze", resumeAt)

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

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateParked, 30*time.Second),
		"worker should park on its first dispatch")

	workerObs := func() []stub.ObservedRequest {
		var out []stub.ObservedRequest
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "worker" {
				out = append(out, o)
			}
		}
		return out
	}
	require.Equal(t, 1, len(workerObs()), "worker invoked exactly once before invalidate")

	scopeID := h.GetMainRunScopeID(iid)
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		latest, err := h.Persist.Nodes().GetLatestRunForNode(context.Background(), tx, worker.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest, "worker should have a parked run before invalidate")
		return h.Persist.NodeAttributes().Upsert(context.Background(), latest.RunID, worker.ID, map[string]any{
			"prior_marker": "from-parked-run",
		}, tx)
	}))

	var newRunID string
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		fr, err := h.Persist.Frames().GetRunningFrameID(context.Background(), iid, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, fr, "instance should have a running frame after parking")
		nid, err := h.Persist.Nodes().CreateNonCascadeStale(context.Background(), tx, persistence.NonCascadeStaleInput{
			NodeID:                 worker.ID,
			RunScopeID:             scopeID,
			FrameID:                *fr,
			ExecutorName:           "stub",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now(),
			CreationReason:         cascade.CreationReasonOperatorInvalidate,
		})
		if err != nil {
			return err
		}
		newRunID = nid.String()
		return nil
	}))

	var staleCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND creation_reason = 'operator_invalidate' AND state = 'stale'`,
		[]any{worker.ID}, &staleCount,
	)
	require.Equal(t, 1, staleCount,
		"operator-invalidate must create exactly one stale row with creation_reason=operator_invalidate")

	var (
		carriedData string
		dispatchBag sql.NullString
	)
	h.QueryRowSQL(
		`SELECT data::text, dispatch_input_bag::text FROM rimsky_node_attributes WHERE node_run_id = $1`,
		[]any{newRunID}, &carriedData, &dispatchBag,
	)
	require.Contains(t, carriedData, "prior_marker",
		"operator-invalidate stale must carry forward the live bag from the most-recent settled/in-flight run")
	require.True(t, dispatchBag.Valid && dispatchBag.String != "",
		"operator-invalidate stale must snapshot a dispatch_input_bag at row creation per the non-cascade-direct-to-stale decision")
	require.Contains(t, dispatchBag.String, "prior_marker",
		"snapshot dispatch_input_bag must mirror the carried-forward live bag")

	require.Equal(t, 1, len(workerObs()),
		"the operator-invalidate stale must NOT dispatch while the parked predecessor is in-flight; "+
			"the dispatcher's serialization gate blocks it")

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "worker-resumed")

	require.True(t, h.WaitForEventKind(worker.ID, "parked_resume_started", 30*time.Second),
		"deadline sweep should wake the parked worker run")

	deadline := time.Now().Add(45 * time.Second)
	var observedAfter int
	for time.Now().Before(deadline) {
		observedAfter = len(workerObs())
		if observedAfter >= 3 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.Equal(t, 3, observedAfter,
		"worker should be invoked exactly three times: (1) parked dispatch, (2) deadline-resume "+
			"of the same row, (3) operator-invalidate stale dispatched after the predecessor settles")

	var creationReasons string
	h.QueryRowSQL(
		`SELECT string_agg(creation_reason, ',' ORDER BY sequence) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{worker.ID}, &creationReasons,
	)
	require.Equal(t, "cascade,operator_invalidate", creationReasons,
		"worker's lineage must show one cascade-driven run (the parked-then-resumed one) "+
			"followed by one operator_invalidate run")
}
