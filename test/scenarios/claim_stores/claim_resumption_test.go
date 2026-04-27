// §19.1 — claim resumption: when an executor crashes mid-run the
// claim ref (the items-table row) is preserved and the same payload
// is re-handed on the next dispatch.
//
// The end-to-end shape (spec §13.6 "Rebind path on next dispatch" + §7.7
// visibility-timeout backstop) has two complementary paths:
//
//  1. **Same supervisor rebind**: the supervisor returns to the node
//     within `resume_grace`. The §13.3-step-3a probe finds the existing
//     `rimsky_lock_holders` row, reuses it, and skips Store.AcquireLock —
//     so the items-table row stays in_progress and the original payload
//     is what OpenHandle echoes back.
//
//  2. **Orphan reap → fresh acquisition**: the supervisor never returns
//     (or is killed). The scheduler's §13.5 step-2 lock-holder sweep
//     reaps the expired row and the visibility-timeout sweep (§13.5
//     step-4) flips the items-table row back to 'available'. A fresh
//     supervisor's AcquireLock then re-picks the SAME items-table row —
//     because it has the smallest enqueued_at (it was claimed first) —
//     and the same payload arrives in the new ClaimResult.
//
// We exercise path (2) at the store level: seed an item, claim it,
// simulate a crash by leaving state='in_progress', then run the
// "visibility timeout reset" step (which the scheduler's sweep does in
// production) and verify the next AcquireLock returns the SAME payload
// and same item_id.
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres.
package claim_stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaimResumption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "resumption_items"
	createItemsTable(t, pool, table)
	s := buildStore(t, pool, "resumption", table, "delete", "release_to_head")

	// Seed one item with a recognisable payload.
	id := insertItem(t, pool, table,
		map[string]any{"area": "north", "subtopic": "otters"}, unsetTime())

	// First acquisition. Claim ref + payload return correctly.
	cr1 := acquireOnce(t, ctx, pool, s)
	require.Equal(t, id.String(), cr1.ClaimID)
	payload1, ok := cr1.Payload.(map[string]any)
	require.True(t, ok, "first claim payload type")
	require.Equal(t, "north", payload1["area"])
	require.Equal(t, "otters", payload1["subtopic"])

	// Items-table row is in_progress.
	state, tok := readItemState(t, pool, table, id)
	require.Equal(t, "in_progress", state)
	require.NotNil(t, tok, "claim_token preserved while in_progress")

	// Simulate "executor crashed mid-run":
	//   - The supervisor's lock-holder row has been reaped by §13.5
	//     step-2 (we're not modelling that here — for the resumption-
	//     payload claim we only need to show the items-table row's
	//     payload is preserved).
	//   - The visibility-timeout sweep (§13.5 step-4) resets the
	//     items-table row to 'available' so a future supervisor can
	//     re-pick it. Mirror that SQL inline.
	_, err := pool.Exec(ctx,
		`UPDATE `+table+`
		   SET state = 'available', claim_token = NULL, claimed_at = NULL
		 WHERE item_id = $1`,
		id,
	)
	require.NoError(t, err)

	// Second acquisition: same row, SAME payload — proving the claim
	// ref (the items-table item_id + payload) survived the crash. This
	// is the resumption guarantee at the store level.
	cr2 := acquireOnce(t, ctx, pool, s)
	require.Equal(t, id.String(), cr2.ClaimID,
		"resumed claim must surface the same item_id (claim ref preserved)")
	payload2, ok := cr2.Payload.(map[string]any)
	require.True(t, ok, "second claim payload type")
	require.Equal(t, payload1, payload2,
		"resumed claim must carry the identical payload")

	// Items-table back to in_progress; the new tx fully consumes the row.
	state2, tok2 := readItemState(t, pool, table, id)
	require.Equal(t, "in_progress", state2)
	require.NotNil(t, tok2)
	require.NotEqual(t, *tok, *tok2,
		"second acquisition must mint a fresh claim_token (concurrency guard)")
}
