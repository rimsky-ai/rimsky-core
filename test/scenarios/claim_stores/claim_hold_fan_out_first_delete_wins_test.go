// §19.1 — held claim, fan-out, first-delete-wins: two terminal-leaf
// nodes hold the same claim; one resolves with `delete`, the other with
// `release`. The first delete wins regardless of resolution order — the
// items-table row is removed and the still-active sibling rows are
// collapsed to `actual_action='delete_won'` (a sentinel that prevents a
// later release from re-firing items-table updates).
//
// We exercise the algorithm in BOTH orderings:
//   - Sub-test "delete-first": nodeA (delete) resolves before nodeB
//     (release). nodeB's holder row collapses to 'delete_won' and no
//     items-table mutation happens on its later resolution.
//   - Sub-test "delete-second": nodeA (release) resolves first. nodeB's
//     later delete still wins because the items-table row is what
//     observers see; the delete branch issues the DELETE and nodeA's
//     row remains 'release_to_back' (already completed).
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres.
package claim_stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

func TestClaimHoldFanOutFirstDeleteWins(t *testing.T) {
	t.Parallel()

	t.Run("delete-first", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		pool := startPostgres(t)

		const table = "fan_delfirst_items"
		createItemsTable(t, pool, table)
		s := buildStore(t, pool, "fan_delfirst", table, "release_to_back", "release_to_head")

		id := insertItem(t, pool, table, map[string]any{"k": "v"}, unsetTime())
		cr := acquireOnce(t, ctx, pool, s)
		require.Equal(t, id.String(), cr.ClaimID)

		_, instID, nodeA := provisionTemplateInstanceNode(t, pool, "delfirst-A")
		nodeB := insertSiblingNode(t, pool, instID, "leaf-delfirst-B")

		hA := insertHolder(t, pool, id.String(), s.Name(), nodeA, "delete", "release_to_head")
		hB := insertHolder(t, pool, id.String(), s.Name(), nodeB, "release_to_back", "release_to_head")

		// nodeA commits → action=delete. The §5.6.4 delete branch
		// removes the items-table row and collapses nodeB to delete_won.
		resolveInTx(t, ctx, pool, s, id.String(), nodeA, claimstorepg.TerminalCommit)
		state, action := readHolder(t, pool, hA)
		require.Equal(t, "completed", state)
		require.Equal(t, "delete", action)
		state, action = readHolder(t, pool, hB)
		require.Equal(t, "completed", state, "nodeB collapses synchronously")
		require.Equal(t, "delete_won", action, "delete-won sentinel must mark the collapsed sibling")

		// Items-table row is gone.
		require.Equal(t, 0, countItemsByState(t, pool, table, "available"))
		require.Equal(t, 0, countItemsByState(t, pool, table, "in_progress"))

		// Late nodeB resolution (a real-world race shape): no-op,
		// idempotent — the row is already 'completed'.
		resolveInTx(t, ctx, pool, s, id.String(), nodeB, claimstorepg.TerminalCommit)
		state2, action2 := readHolder(t, pool, hB)
		require.Equal(t, "completed", state2)
		require.Equal(t, "delete_won", action2, "late re-resolve must be a no-op")
	})

	t.Run("delete-second", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		pool := startPostgres(t)

		const table = "fan_delsecond_items"
		createItemsTable(t, pool, table)
		s := buildStore(t, pool, "fan_delsecond", table, "release_to_back", "release_to_head")

		id := insertItem(t, pool, table, map[string]any{"k": "v"}, unsetTime())
		cr := acquireOnce(t, ctx, pool, s)
		require.Equal(t, id.String(), cr.ClaimID)

		_, instID, nodeA := provisionTemplateInstanceNode(t, pool, "delsecond-A")
		nodeB := insertSiblingNode(t, pool, instID, "leaf-delsecond-B")

		// Reverse roles: nodeA releases, nodeB deletes.
		hA := insertHolder(t, pool, id.String(), s.Name(), nodeA, "release_to_back", "release_to_head")
		hB := insertHolder(t, pool, id.String(), s.Name(), nodeB, "delete", "release_to_head")

		// nodeA commits first with release_to_back. ACTIVE_COUNT > 0
		// (nodeB still active) so no items-table mutation fires.
		resolveInTx(t, ctx, pool, s, id.String(), nodeA, claimstorepg.TerminalCommit)
		state, action := readHolder(t, pool, hA)
		require.Equal(t, "completed", state)
		require.Equal(t, "release_to_back", action)
		itemState, _ := readItemState(t, pool, table, id)
		require.Equal(t, "in_progress", itemState,
			"items-table must remain in_progress while nodeB is still active")

		// nodeB commits second with delete. The delete branch sees no
		// PRIOR_DELETE (nodeA's actual_action is release_to_back, not
		// 'delete') and issues the items-table DELETE. nodeA's row stays
		// 'completed/release_to_back' — not collapsed, since it's
		// already completed at delete time.
		resolveInTx(t, ctx, pool, s, id.String(), nodeB, claimstorepg.TerminalCommit)
		state, action = readHolder(t, pool, hB)
		require.Equal(t, "completed", state)
		require.Equal(t, "delete", action)
		stateA2, actionA2 := readHolder(t, pool, hA)
		require.Equal(t, "completed", stateA2,
			"nodeA's holder row stays completed (delete does not collapse already-completed siblings)")
		require.Equal(t, "release_to_back", actionA2)

		// Items-table row gone — delete wins regardless of order.
		require.Equal(t, 0, countItemsByState(t, pool, table, "available"))
		require.Equal(t, 0, countItemsByState(t, pool, table, "in_progress"))
	})
}
