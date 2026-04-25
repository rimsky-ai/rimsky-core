// Scenario 17 — stub completes with changed=false; commit writes a
// no_op_commit event, resource.current_version_id is unchanged, and
// dependents are NOT cascaded.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func TestNoOpCommit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// First producer run commits normally so the dependent can reach fresh.
	h.Stub.WhenType("producer").Complete(map[string]any{"x": 1}, true, "initial")
	h.Stub.WhenType("dependent").Complete(map[string]any{"y": 2}, true, "downstream")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "noop", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "producer", Executor: "stub",
				OwnsResources: []node.ResourceDef{
					{Path: []string{"db", "{consumer_key}"}, Implementation: "inline-jsonb"},
				},
			},
			{Type: "dependent", Executor: "stub", Dependencies: []string{"producer"}},
		},
	})
	iid := h.CreateInstance(tid, "ck-noop", map[string]any{})

	producer := h.FindNode(iid, "producer")
	dep := h.FindNode(iid, "dependent")
	require.NotNil(t, producer)
	require.NotNil(t, dep)

	// Both reach fresh after first run.
	require.True(t, h.WaitForNodeState(producer.ID, shared.NodeStateFresh, 60*time.Second),
		"producer did not reach fresh")
	require.True(t, h.WaitForNodeState(dep.ID, shared.NodeStateFresh, 60*time.Second),
		"dependent did not reach fresh on first cascade")

	resources, err := h.Storage.Resources().ListByOwner(h.Ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	firstVersion := resources[0].CurrentVersionID
	require.NotNil(t, firstVersion, "first run must have committed a version")

	// Now swap stub to no_op and invalidate producer; it should re-run and
	// terminate as a no_op_commit.
	h.Stub.WhenType("producer").Complete(map[string]any{"x": 1}, false, "noop")
	require.NoError(t, h.Storage.Nodes().UpdateState(h.Ctx, producer.ID,
		shared.NodeStateStale, node.ReasonOperatorInvalidate, nil))

	// Wait for producer to return to fresh.
	require.True(t, h.WaitForNodeState(producer.ID, shared.NodeStateFresh, 60*time.Second),
		"producer did not re-reach fresh after no_op")

	// current_version_id unchanged.
	refreshed, err := h.Storage.Resources().Get(h.Ctx, resources[0].ID, nil)
	require.NoError(t, err)
	require.NotNil(t, refreshed.CurrentVersionID)
	require.Equal(t, *firstVersion, *refreshed.CurrentVersionID,
		"no_op commit must not bump current_version")

	// no_op_commit event recorded.
	nid := producer.ID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid, Kind: "no_op_commit"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected no_op_commit event")

	// Dependent was not re-cascaded: there should be NO pending dispatch row
	// for it. (It already ran once and stayed fresh.)
	var depDispatchCount int
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_dispatch WHERE node_id = $1`, dep.ID,
	).Scan(&depDispatchCount)
	require.NoError(t, err)
	require.Equal(t, 0, depDispatchCount,
		"dependent should not be re-enqueued after producer no_op commit")

	depGot, err := h.Storage.Nodes().Get(h.Ctx, dep.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, depGot.State,
		"dependent should still be fresh (never cascaded)")
}
