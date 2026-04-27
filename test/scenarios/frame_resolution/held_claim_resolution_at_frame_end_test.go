// Verifies spec §6 + §10.4: the rimsky_claim_holders.frame_id
// observability column is populated by the canonical Insert helper and
// round-trips through the storage interface; the existing PK
// (claim_id, holder_node_id) is unchanged. Resolution paths
// (§5.6.4 algorithm) do NOT key on frame_id.
//
// This is a focused observability-column test. Full-stack held-claim
// scenarios under the frame model are exercised by the existing
// test/scenarios/claim_stores/* suite (which the agent confirmed pass
// post-frame-resolution).
package frame_resolution

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func TestHeldClaimResolutionAtFrameEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-frame-id", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-claim-frame", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Create a synthetic running frame to associate the claim_holder row with.
	var frameID uuid.UUID
	err := h.Pool.QueryRow(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
		ON CONFLICT (instance_id) WHERE state = 'running' DO NOTHING
		RETURNING frame_id
	`, uuid.UUID(iid), uuid.UUID(worker.ID)).Scan(&frameID)
	if err != nil {
		// A running frame may already exist (CreateInstance auto-enqueued one).
		err = h.Pool.QueryRow(h.Ctx, `
			SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state IN ('queued','running')
			ORDER BY queued_at DESC LIMIT 1
		`, uuid.UUID(iid)).Scan(&frameID)
		require.NoError(t, err)
	}

	// Insert a rimsky_claim_holders row through the canonical helper.
	holderID := shared.UUID(uuid.New())
	frameIDShared := shared.UUID(frameID)
	err = h.Storage.ClaimHolders().Insert(h.Ctx, storage.ClaimHolderInsertInput{
		ID:           holderID,
		ClaimID:      "synthetic-claim-id-1",
		StoreName:    "stub_claim_store",
		HolderNodeID: worker.ID,
		OnCommit:     "release_to_back",
		OnGiveUp:     "release_to_head",
		FrameID:      &frameIDShared,
	}, nil)
	require.NoError(t, err)

	// Read it back through the canonical SELECT (claim_holder_cols).
	row, err := h.Storage.ClaimHolders().Get(h.Ctx, holderID, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "synthetic-claim-id-1", row.ClaimID)
	require.Equal(t, worker.ID, row.HolderNodeID)
	require.Equal(t, storage.ClaimHolderState("active"), row.State)

	// Read frame_id directly via SQL (it's not in the storage row struct
	// because it's observability-only).
	var observedFrameID uuid.UUID
	err = h.Pool.QueryRow(context.Background(),
		`SELECT frame_id FROM rimsky_claim_holders WHERE id = $1`, uuid.UUID(holderID)).Scan(&observedFrameID)
	require.NoError(t, err)
	require.Equal(t, frameID, observedFrameID,
		"rimsky_claim_holders.frame_id must round-trip through Insert")

	// PK semantics: claim_holders_claim_node_idx is (claim_id, holder_node_id),
	// no frame_id. A second Insert with same (claim_id, holder_node_id) but
	// different frame_id should violate the unique index.
	differentFrame := shared.UUID(uuid.New())
	err = h.Storage.ClaimHolders().Insert(h.Ctx, storage.ClaimHolderInsertInput{
		ID:           shared.UUID(uuid.New()),
		ClaimID:      "synthetic-claim-id-1",
		StoreName:    "stub_claim_store",
		HolderNodeID: worker.ID,
		OnCommit:     "release_to_back",
		OnGiveUp:     "release_to_head",
		FrameID:      &differentFrame,
	}, nil)
	require.Error(t, err,
		"second insert on same (claim_id, holder_node_id) must fail; frame_id is NOT part of the unique index")
}
