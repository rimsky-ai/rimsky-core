// §19.1 — queue on_commit=delete: a successful commit on a queue-shaped
// claim store atomically removes the items-table row.
//
// "Atomically" here means: the items-table DELETE runs inside the same
// transaction as the lock-holder release (spec §13.6 release tx). The
// non-held commit path issues `DELETE FROM <items_table>` directly; the
// store's `ReleaseClaimItem(... "delete")` is rejected by design (delete
// is owned by the §5.6.4 algorithm or by the supervisor's commit code,
// not by the store-side reposition method — see
// `core/store/claimstorepg/release.go`).
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres.
package claim_stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueueOnCommitDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "ack_delete_items"
	createItemsTable(t, pool, table)
	// Queue defaults: on_commit=delete, on_give_up=release_to_head.
	s := buildStore(t, pool, "ack_delete", table, "delete", "release_to_head")

	id := insertItem(t, pool, table, map[string]any{"work": "task-1"}, unsetTime())

	cr := acquireOnce(t, ctx, pool, s)
	require.Equal(t, id.String(), cr.ClaimID)

	// Items-table is now in_progress with a non-null claim_token.
	state, tok := readItemState(t, pool, table, id)
	require.Equal(t, "in_progress", state)
	require.NotNil(t, tok, "in_progress rows must carry a claim_token")

	// Commit path: the supervisor's commit_test.go fixture issues a
	// DELETE inside the release tx. Mirror that here so we exercise the
	// real "ack atomically" claim — the row vanishes from the items table
	// and the per-state count drops to zero.
	deleteItemInTx(t, ctx, pool, table, id.String())

	require.Equal(t, 0, countItemsByState(t, pool, table, "in_progress"),
		"after on_commit=delete, no in_progress rows must remain")
	require.Equal(t, 0, countItemsByState(t, pool, table, "available"),
		"after on_commit=delete, no available rows must remain (queue ack)")

	// The store's own ReleaseClaimItem path correctly rejects 'delete'
	// to keep the two callers separate. Use a fresh row to exercise that
	// rejection (we already deleted the original).
	id2 := insertItem(t, pool, table, map[string]any{"work": "task-2"}, unsetTime())
	cr2 := acquireOnce(t, ctx, pool, s)
	require.Equal(t, id2.String(), cr2.ClaimID)
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.Error(t,
		s.ReleaseClaimItem(withTxCtx(ctx, tx), id2.String(), "delete"),
		"ReleaseClaimItem must reject 'delete' (owned by §5.6.4 algorithm / supervisor)")
}
