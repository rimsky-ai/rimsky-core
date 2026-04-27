// §19.1 — claimant-guarded release: deleting a rimsky_lock_holders row
// with the wrong holder_supervisor_id is a no-op.
//
// Verifies blessed invariant 4 (spec §18 invariant 4): "Claimant-guarded
// release. Every DELETE FROM rimsky_lock_holders is `AND … =
// supervisor_id`." Threading SupervisorID through every release path is
// what prevents stale orphan sweeps from nulling live claims.
//
// Test mechanism: insert a lock-holder row owned by `real-sup`. Call
// LockHolders().Delete with `wrong-sup` as the expected supervisor —
// verify the row remains. Then call Delete with `real-sup` — verify
// the row is deleted.
package locks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/storage"
)

func TestLockClaimantGuardedRelease(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	// One node so we have a real holder_node_id to anchor the lock.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claimant-guarded", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "anchor", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-guarded", map[string]any{})

	anchor := h.FindNode(iid, "anchor")
	require.NotNil(t, anchor)

	// Insert a lock-holder row owned by `real-sup` with a long TTL.
	holderID := uuid.New()
	lockName := "guarded-mutex"
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 holderID,
			LockKind:           storage.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "real-sup",
			HolderNodeID:       anchor.ID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}))

	// Mismatched-supervisor delete is a no-op (no error, row remains).
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Delete(ctx, holderID, "wrong-sup", tx)
	}), "Delete with wrong supervisor must not return an error (no-op)")

	got, err := h.Storage.LockHolders().Get(h.Ctx, holderID, nil)
	require.NoError(t, err)
	require.NotNil(t, got, "lock-holder row must survive a wrong-supervisor delete attempt")
	require.Equal(t, "real-sup", got.HolderSupervisorID)

	// Correct-supervisor delete removes the row.
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Delete(ctx, holderID, "real-sup", tx)
	}))

	got, err = h.Storage.LockHolders().Get(h.Ctx, holderID, nil)
	require.NoError(t, err)
	require.Nil(t, got, "lock-holder row should be deleted after a correct-supervisor delete")
}
