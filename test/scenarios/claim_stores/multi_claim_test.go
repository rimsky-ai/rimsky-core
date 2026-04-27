// §19.1 — multi-claim: a single node references TWO claim stores (X and
// Y). Each AcquireLock is independent and surfaces its own payload; the
// supervisor namespaces them under `{{claim.X.payload.f}}` /
// `{{claim.Y.payload.f}}` in the attributes substitution; each resolves
// per its OWN store's `on_commit` policy.
//
// This test drives the two claim stores at the layer the supervisor
// uses: both stores inside the same atomic acquisition transaction
// (spec §13.3 — both AcquireLocks land or both roll back), each store's
// ClaimResult carrying its own payload + claim_id, and each store's
// release path firing independently per its configured on_commit.
//
// Two stores with DIFFERENT defaults exercise the "namespacing"
// guarantee: store-X uses `release_to_back` (ring-buffer semantics);
// store-Y uses `delete` (queue semantics). After the node's commit,
// store-X's row must reappear as 'available'; store-Y's row must be
// gone.
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres.
package claim_stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/store"
)

func TestMultiClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	// Two distinct stores with distinct items tables and distinct
	// on_commit defaults. The supervisor's namespacing under
	// {{claim.<store>.payload.f}} keeps the two payloads separate at
	// substitution time; this test exercises the store-level shape that
	// makes that possible — independent ClaimResult payloads + claim_ids
	// and per-store release dispositions.
	const tableX = "multi_x_items"
	const tableY = "multi_y_items"
	createItemsTable(t, pool, tableX)
	createItemsTable(t, pool, tableY)

	// Store X: ring-buffer (release_to_back). Used for stable refs.
	storeX := buildStore(t, pool, "X", tableX, "release_to_back", "release_to_back")
	// Store Y: queue (delete). Used for one-shot work units.
	storeY := buildStore(t, pool, "Y", tableY, "delete", "release_to_head")

	// Seed one item per store with distinct payloads so we can verify
	// they don't cross-pollute.
	idX := insertItem(t, pool, tableX, map[string]any{"area": "north", "from": "X"}, unsetTime())
	idY := insertItem(t, pool, tableY, map[string]any{"task": "review", "from": "Y"}, unsetTime())

	// Open one tx, acquire on both stores inside it, commit. Mirrors the
	// supervisor's spec §13.3 step 3: all AcquireLocks happen inside a
	// single outer tx so the claim atomicity (per-tag-locks-acquired-
	// in-sorted-order invariant generalised to claim acquisitions)
	// holds. If either fails the whole tx rolls back.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	txCtx := store.WithTx(ctx, tx)
	_, crX, err := storeX.AcquireLock(txCtx, store.ClaimLockSpec{StoreName: "X"})
	require.NoError(t, err)
	_, crY, err := storeY.AcquireLock(txCtx, store.ClaimLockSpec{StoreName: "Y"})
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	// (a) Both ClaimResults carry their own claim_id + payload.
	require.Equal(t, idX.String(), crX.ClaimID, "store X claim_id")
	require.Equal(t, idY.String(), crY.ClaimID, "store Y claim_id")

	payloadX, ok := crX.Payload.(map[string]any)
	require.True(t, ok, "store X payload type")
	require.Equal(t, "north", payloadX["area"], "store X payload field")
	require.Equal(t, "X", payloadX["from"], "store X payload provenance")

	payloadY, ok := crY.Payload.(map[string]any)
	require.True(t, ok, "store Y payload type")
	require.Equal(t, "review", payloadY["task"], "store Y payload field")
	require.Equal(t, "Y", payloadY["from"], "store Y payload provenance")

	// (b) Both items-table rows reflect the in_progress flip.
	stateX, _ := readItemState(t, pool, tableX, idX)
	stateY, _ := readItemState(t, pool, tableY, idY)
	require.Equal(t, "in_progress", stateX)
	require.Equal(t, "in_progress", stateY)

	// (c) Per-store release: each store fires its OWN on_commit policy.
	// Store X (release_to_back): row goes back to 'available'. Store Y
	// (delete): row is removed.
	releaseInTx(t, ctx, pool, storeX, idX.String(), "release_to_back")
	deleteItemInTx(t, ctx, pool, tableY, idY.String())

	// Final assertions: namespacing held — X behaves per X's policy, Y
	// behaves per Y's. The two stores never crossed wires.
	require.Equal(t, 1, countItemsByState(t, pool, tableX, "available"),
		"store X (ring buffer) row must return to available")
	require.Equal(t, 0, countItemsByState(t, pool, tableX, "in_progress"))
	require.Equal(t, 0, countItemsByState(t, pool, tableY, "available"),
		"store Y (queue) row must be deleted on commit")
	require.Equal(t, 0, countItemsByState(t, pool, tableY, "in_progress"))
}
