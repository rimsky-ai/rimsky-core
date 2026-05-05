// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 17 — stub completes with changed=false; supervisor records a
// `no_op_commit` event (preserved kind, spec §16) and does NOT emit
// `attributes_committed`. Dependents are NOT cascaded.
//
// Migrated to the stores-redesign template grammar (spec §11). The legacy
// `current_version_id` assertion is gone — the redesign retired the
// `rimsky_resource_versions` table; the post-redesign signal that a commit
// did/did-not write data is the event kind, not a version pointer.
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestNoOpCommit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// First producer run commits real data so the dependent reaches fresh.
	h.Stub.WhenType("producer").Complete(map[string]any{"x": 1}, true, "initial")
	h.Stub.WhenType("dependent").Complete(map[string]any{"y": 2}, true, "downstream")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "noop", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "integer"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:         "dependent",
					Executor:     "stub",
					Dependencies: []string{"producer"},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"y": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-noop", map[string]any{})

	producer := h.FindNode(iid, "producer")
	dep := h.FindNode(iid, "dependent")
	require.NotNil(t, producer)
	require.NotNil(t, dep)

	require.True(t, h.WaitForNodeState(producer.ID, shared.NodeStateFresh, 60*time.Second),
		"producer did not reach fresh")
	require.True(t, h.WaitForNodeState(dep.ID, shared.NodeStateFresh, 60*time.Second),
		"dependent did not reach fresh on first cascade")

	// Capture the dependent's last-updated time so we can later assert the
	// no-op producer commit didn't drag it back through running.
	depBefore, err := h.Persist.Nodes().Get(h.Ctx, dep.ID, nil)
	require.NoError(t, err)

	// Swap the stub to changed=false and invalidate the producer; the
	// supervisor should re-run it, emit `no_op_commit`, NOT emit
	// `attributes_committed`, and NOT cascade.
	h.Stub.WhenType("producer").Complete(map[string]any{"x": 1}, false, "noop")

	// Snapshot the producer's existing attributes_committed event count so a
	// pre-existing committed event from the first run doesn't false-positive
	// the assertion below.
	pid := producer.ID
	priorCommitted, err := h.Persist.Events().List(h.Ctx,
		persistence.EventListFilter{NodeID: &pid, Kind: "attributes_committed"},
		persistence.ListPagination{Limit: 200}, nil)
	require.NoError(t, err)
	priorCount := len(priorCommitted.Events)

	// Under frame resolution, operator-driven invalidation goes through
	// the controlapi → InvalidateNode → frame.EnqueueOrCoalesce path.
	// A direct UpdateState bypasses the frame engine and leaves the
	// node stale-with-nil-frame_id.
	resp, err := http.Post(h.ControlBase+"/nodes/"+producer.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for the no_op_commit event to be emitted by the second run.
	// We can't WaitForNodeState(fresh) here because the node may already
	// be fresh from the first run; the new run cycles fresh→stale→fresh.
	require.True(t, h.WaitForEventKind(producer.ID, "no_op_commit", 60*time.Second),
		"producer did not emit no_op_commit after changed=false run")

	// `no_op_commit` event recorded for the producer.
	noOpEvs, err := h.Persist.Events().List(h.Ctx,
		persistence.EventListFilter{NodeID: &pid, Kind: "no_op_commit"},
		persistence.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, noOpEvs.Events, "expected no_op_commit event after changed=false run")

	// No NEW `attributes_committed` event was emitted by the second run.
	postCommitted, err := h.Persist.Events().List(h.Ctx,
		persistence.EventListFilter{NodeID: &pid, Kind: "attributes_committed"},
		persistence.ListPagination{Limit: 200}, nil)
	require.NoError(t, err)
	require.Equal(t, priorCount, len(postCommitted.Events),
		"no_op commit must NOT emit attributes_committed (changed=false)")

	// Dependent was not re-cascaded: no pending dispatch row, still fresh,
	// state unchanged since first cascade.
	var depDispatchCount int
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_worker_request WHERE node_id = $1`, dep.ID,
	).Scan(&depDispatchCount)
	require.NoError(t, err)
	require.Equal(t, 0, depDispatchCount,
		"dependent should not be re-enqueued after producer no_op commit")

	depAfter, err := h.Persist.Nodes().Get(h.Ctx, dep.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, depAfter.State,
		"dependent should still be fresh (never cascaded)")
	require.Equal(t, depBefore.UpdatedAt, depAfter.UpdatedAt,
		"dependent's updated_at should not advance on a producer no_op commit")
}
