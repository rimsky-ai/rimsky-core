// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 17 — stub completes with changed=false; supervisor records a
// `no_op_commit` event (preserved kind, spec §16) and does NOT emit
// `attributes_committed`.
//
// Post-2026-05-14 the pessimistic-invalidate rule (spec Piece 1) marks
// subscribers stale at the producer's INVALIDATION transition (not at
// settlement). A subscriber re-dispatches idempotently per
// `concept:wait-set`'s "filter didn't actually match" rule — the
// settled-state drain releases the wait-set unconditionally. The
// old-model assertion that "dependent should still be fresh" no longer
// holds; this test now asserts the producer's no_op_commit semantic
// (event-kind preserved; no attributes_committed) and the dependent's
// idempotent re-dispatch through the cascade.
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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

func TestNoOpCommit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// First producer run commits real data so the dependent reaches fresh.
	h.Stub.WhenType("producer").Success(map[string]any{"x": 1}, true, "initial")
	h.Stub.WhenType("dependent").Success(map[string]any{"y": 2}, true, "downstream")

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
					Type:     "dependent",
					Executor: "stub",
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "producer", On: "state"}),
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

	require.True(t, h.WaitForNodeState(producer.ID, cascade.NodeStateFresh, 60*time.Second),
		"producer did not reach fresh")
	require.True(t, h.WaitForNodeState(dep.ID, cascade.NodeStateFresh, 60*time.Second),
		"dependent did not reach fresh on first cascade")

	// Capture the dependent's last-updated time so we can later assert the
	// no-op producer commit didn't drag it back through running.
	var depBefore *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, dep.ID, tx)
		depBefore = r
		return err
	}))

	// Swap the stub to changed=false and invalidate the producer; the
	// supervisor should re-run it, emit `no_op_commit`, NOT emit
	// `attributes_committed`, and NOT cascade.
	h.Stub.WhenType("producer").Success(map[string]any{"x": 1}, false, "noop")

	// Snapshot the producer's existing attributes_committed event count so a
	// pre-existing committed event from the first run doesn't false-positive
	// the assertion below.
	pid := producer.ID
	var priorCommitted persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &pid, Kind: "attributes_committed"},
			persistence.ListPagination{Limit: 200}, tx)
		priorCommitted = r
		return err
	}))
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
	var noOpEvs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &pid, Kind: "no_op_commit"},
			persistence.ListPagination{Limit: 10}, tx)
		noOpEvs = r
		return err
	}))
	require.NotEmpty(t, noOpEvs.Events, "expected no_op_commit event after changed=false run")

	// No NEW `attributes_committed` event was emitted by the second run.
	var postCommitted persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &pid, Kind: "attributes_committed"},
			persistence.ListPagination{Limit: 200}, tx)
		postCommitted = r
		return err
	}))
	require.Equal(t, priorCount, len(postCommitted.Events),
		"no_op commit must NOT emit attributes_committed (changed=false)")

	// Under the pessimistic-invalidate rule, the producer's
	// invalidation cascades to mark the dependent stale; the
	// dependent re-dispatches idempotently. Wait for the dependent
	// to settle back to fresh — the test's stub returns the same
	// downstream attributes, so the dependent's second run is
	// semantically a no-op too. We assert the cascade completes
	// (dependent re-reaches fresh) rather than asserting the
	// dependent was never re-cascaded.
	require.True(t, h.WaitForNodeState(dep.ID, cascade.NodeStateFresh, 30*time.Second),
		"dependent should re-reach fresh after idempotent cascade from producer no_op commit")

	// depBefore is captured pre-invalidation; not asserting on
	// UpdatedAt anymore since the dependent's re-dispatch advances it.
	_ = depBefore
}
