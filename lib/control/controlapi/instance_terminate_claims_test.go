// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: claim-handle
// @decision: held-as-state-not-phase

package controlapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func seedNodeRunInState(
	ctx context.Context, t *testing.T, h *harness,
	nodeID, frameID, runScopeID shared.UUID, state string,
) shared.UUID {
	t.Helper()
	var runID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, h.driver,
		`INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_claim_producers, enqueued_at, state, frame_id, run_scope_id, sequence)
         VALUES (gen_random_uuid(), $1, 'worker', ARRAY[]::text[], now(), $2, $3, $4, 0)
         RETURNING id`,
		[]any{uuid.UUID(nodeID), state, uuid.UUID(frameID), uuid.UUID(runScopeID)}, &runID,
	)
	return runID
}

func TestTerminateInstance_ForceCancelsHeldAndInFlightClaims(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "claims-terminate")
	instUUID := mustParseUUID(t, instID)
	rootNode := findNodeIDByType(t, h, instUUID, "root")
	childNode := findNodeIDByType(t, h, instUUID, "child")

	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/claims-terminate")
	var runScopeID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, frameRow)
		runScopeID = frameRow.RootRunScopeID
		return nil
	}))

	heldRunID := seedNodeRunInState(ctx, t, h, rootNode.ID, frameID, runScopeID, "held")
	runningRunID := seedNodeRunInState(ctx, t, h, childNode.ID, frameID, runScopeID, "running")

	heldProducer := "test-store"
	heldIntent := "rw"
	heldHandleID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 shared.UUID(heldHandleID),
			LockKind:           persistence.LockKindScope,
			ProducerName:       &heldProducer,
			ClaimScopeData:     []byte(`"held-scope"`),
			Intent:             &heldIntent,
			HolderSupervisorID: "test-sup-held",
			HolderNodeID:       rootNode.ID,
			NodeRunID:          &heldRunID,
			IsHeld:             true,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
			FrameID:            &frameID,
		}, tx)
	}))

	holderRowID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              shared.UUID(holderRowID),
			ClaimHandleID:   shared.UUID(heldHandleID),
			HolderNodeRunID: heldRunID,
		}, tx)
	}))

	runningProducer := "test-store"
	runningIntent := "rw"
	runningHandleID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 shared.UUID(runningHandleID),
			LockKind:           persistence.LockKindScope,
			ProducerName:       &runningProducer,
			ClaimScopeData:     []byte(`"running-scope"`),
			Intent:             &runningIntent,
			HolderSupervisorID: "test-sup-running",
			HolderNodeID:       childNode.ID,
			NodeRunID:          &runningRunID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
			FrameID:            &frameID,
		}, tx)
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/terminate", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		holderRow, err := h.persist.ClaimHolders().Get(ctx, shared.UUID(holderRowID), tx)
		if err != nil {
			return err
		}
		require.NotNil(t, holderRow)
		require.Equal(t, persistence.ClaimHolderStateFailed, holderRow.State,
			"force-killing an instance must fail the active co-holder row of a held claim (poison rule); "+
				"a claimant-guard mismatch on the sentinel supervisor id would leave this row active")
		return nil
	}))

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		runningHandle, err := h.persist.ClaimHandles().Get(ctx, shared.UUID(runningHandleID), tx)
		if err != nil {
			return err
		}
		require.NotNil(t, runningHandle)
		require.Equal(t, spec.ClaimHandleStateAbandoned, runningHandle.State,
			"force-killing an instance must abandon a still-active in-flight claim held by a running node")
		return nil
	}))
}
