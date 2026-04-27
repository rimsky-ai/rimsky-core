// §19.1 — queue on_give_up=release_to_head: when a node holding a
// queue-shaped claim gives up, the items-table row is repositioned to
// the head of the FIFO order so the next supervisor sees it first.
//
// `release_to_head` is implemented as `enqueued_at = now() - INTERVAL '1
// year'` (`core/store/claimstorepg/release.go::releaseToHeadShift`); the
// row sorts before any wall-clock-real `enqueued_at` value so the next
// AcquireLock picks it up immediately.
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres.
package claim_stores

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueueOnGiveUpReleaseToHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "givehead_items"
	createItemsTable(t, pool, table)
	// Queue defaults per spec §7.4: on_give_up=release_to_head.
	s := buildStore(t, pool, "givehead", table, "delete", "release_to_head")

	// Insert two items in FIFO order: first → "first", second → "second".
	base := time.Now().UTC()
	first := insertItem(t, pool, table, map[string]any{"name": "first"}, base)
	second := insertItem(t, pool, table, map[string]any{"name": "second"}, base.Add(time.Millisecond))

	// Claim "first" — items-table flips to in_progress.
	cr := acquireOnce(t, ctx, pool, s)
	require.Equal(t, first.String(), cr.ClaimID)

	// Give-up via release_to_head: items-table row goes back to
	// 'available' with enqueued_at far in the past.
	releaseInTx(t, ctx, pool, s, first.String(), "release_to_head")

	// Inspect the row directly: state=available, claim_token=null,
	// enqueued_at far in the past.
	state, tok := readItemState(t, pool, table, first)
	require.Equal(t, "available", state)
	require.Nil(t, tok, "released rows must clear claim_token")

	var enqAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT enqueued_at FROM `+table+` WHERE item_id = $1`, first,
	).Scan(&enqAt))
	require.True(t, enqAt.Before(time.Now().Add(-30*24*time.Hour)),
		"release_to_head must push enqueued_at far into the past, got %v", enqAt)

	// Next AcquireLock must surface "first" again (sorts ahead of "second"
	// because its enqueued_at is now far older).
	got := acquireOnce(t, ctx, pool, s)
	require.Equal(t, first.String(), got.ClaimID,
		"release_to_head must place the row at the head of the next claim")

	// "second" should be claimed only after "first" is fully drained.
	got2 := acquireOnce(t, ctx, pool, s)
	require.Equal(t, second.String(), got2.ClaimID)
}
