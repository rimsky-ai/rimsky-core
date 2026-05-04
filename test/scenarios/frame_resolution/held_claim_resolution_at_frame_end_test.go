// Verifies the stores redesign ClaimHolderInsertInput shape round-
// trips through the storage interface and the (lock_holder_id,
// holder_node_id) uniqueness constraint holds.
//
// The pre-redesign test in this position exercised ClaimID/StoreName/
// OnCommit/OnGiveUp/FrameID columns on rimsky_claim_holders — all
// removed by the redesign. Per-row resolution actions now live in
// template metadata; rows simply record subgraph membership and
// per-member terminal state (auto-terminal: spec §4.10 invariant 13).
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestHeldClaimRowRoundTrip(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-claim-roundtrip", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-claim-roundtrip", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Seed a region-kind lock-holder anchored to worker; needed to
	// satisfy the FK on rimsky_claim_holders.lock_holder_id. Both
	// inserts run inside a single Tx — required by Insert per §7.3.
	storeName := "scenario-store"
	intent := "rw"
	lockHolderID := shared.UUID(uuid.New())
	holderID := shared.UUID(uuid.New())
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.Persist.LockHolders().Insert(ctx, persistence.LockHolderInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindScope,
			StoreName:          &storeName,
			ScopeData:          []byte(`"r-1"`),
			Intent:             &intent,
			HolderSupervisorID: "scenario-supervisor",
			HolderNodeID:       worker.ID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
		}, tx); err != nil {
			return err
		}
		return h.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:           holderID,
			LockHolderID: lockHolderID,
			HolderNodeID: worker.ID,
		}, tx)
	}))

	row, err := h.Persist.ClaimHolders().Get(h.Ctx, holderID, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, lockHolderID, row.LockHolderID)
	require.Equal(t, worker.ID, row.HolderNodeID)
	require.Equal(t, persistence.ClaimHolderStateActive, row.State)

	// Second insert on same (lock_holder_id, holder_node_id) must fail
	// per the §12.11 unique index.
	err = h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:           shared.UUID(uuid.New()),
			LockHolderID: lockHolderID,
			HolderNodeID: worker.ID,
		}, tx)
	})
	require.Error(t, err)
}
