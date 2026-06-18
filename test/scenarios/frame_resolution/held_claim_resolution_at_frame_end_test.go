// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// per-member terminal state (auto-terminal: `@blessed-invariant 13`).
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHeldClaimRowRoundTrip(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-roundtrip", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-claim-roundtrip", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	var frameID shared.UUID
	h.QueryRowSQL(`
		SELECT frame_id FROM rimsky_frames WHERE instance_id = $1
		 ORDER BY queued_at DESC LIMIT 1
	`, []any{uuid.UUID(iid)}, &frameID)
	h.ExecSQL(`UPDATE rimsky_frames SET state = 'running', started_at = COALESCE(started_at, now()),
		         last_progress_at = COALESCE(last_progress_at, now())
		   WHERE frame_id = $1`, frameID)
	h.ExecSQL(`UPDATE rimsky_nodes SET frame_id = $1, updated_at = now() WHERE id = $2`,
		frameID, uuid.UUID(worker.ID))
	var runID shared.UUID
	h.QueryRowSQL(`
		SELECT id FROM rimsky_node_runs
		 WHERE node_id = $1 AND frame_id = $2
		   AND phase IN ('pending','active','held','parked')
		 ORDER BY enqueued_at DESC LIMIT 1
	`, []any{uuid.UUID(worker.ID), frameID}, &runID)

	producerName := "scenario-store"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	holderID := shared.UUID(uuid.New())
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"r-1"`),
			Intent:             &intent,
			HolderSupervisorID: "scenario-supervisor",
			HolderNodeID:       worker.ID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
		}, tx); err != nil {
			return err
		}
		return h.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            holderID,
			ClaimHandleID: lockHolderID,
			HolderRunID:   runID,
		}, tx)
	}))

	var row *persistence.ClaimHolderRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.ClaimHolders().Get(h.Ctx, holderID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, lockHolderID, row.ClaimHandleID)
	require.Equal(t, runID, row.HolderRunID)
	require.Equal(t, persistence.ClaimHolderStateActive, row.State)

	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            shared.UUID(uuid.New()),
			ClaimHandleID: lockHolderID,
			HolderRunID:   runID,
		}, tx)
	})
	require.Error(t, err)
}
