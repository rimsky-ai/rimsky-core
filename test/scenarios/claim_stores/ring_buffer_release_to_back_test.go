// §19.1 — ring buffer on_commit=release_to_back: a successful commit on
// a ring-buffer-shaped claim store returns the items-table row to the
// back of the FIFO order so it becomes claimable again after the rest
// of the buffer drains.
//
// `release_to_back` stamps `enqueued_at=now()`
// (`core/store/claimstorepg/release.go::repositionToBack`). The row
// sorts after all currently-available rows whose enqueued_at is older.
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

func TestRingBufferReleaseToBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "ring_back_items"
	createItemsTable(t, pool, table)
	// Ring buffer defaults per spec §7.4: on_commit=release_to_back,
	// on_give_up=release_to_back.
	s := buildStore(t, pool, "ring_back", table, "release_to_back", "release_to_back")

	// Three items, FIFO order: first/second/third. Stamp enqueued_at
	// well in the past so subsequent `release_to_back` calls (which
	// stamp now()) reliably sort AFTER all original items even if the
	// postgres container's clock skews against the host's.
	base := time.Now().UTC().Add(-1 * time.Hour)
	first := insertItem(t, pool, table, map[string]any{"name": "first"}, base)
	second := insertItem(t, pool, table, map[string]any{"name": "second"}, base.Add(time.Second))
	third := insertItem(t, pool, table, map[string]any{"name": "third"}, base.Add(2*time.Second))

	// Claim "first"; commit via release_to_back. The ring buffer never
	// deletes — the row goes to the back of the queue.
	cr := acquireOnce(t, ctx, pool, s)
	require.Equal(t, first.String(), cr.ClaimID)
	releaseInTx(t, ctx, pool, s, first.String(), "release_to_back")

	state, tok := readItemState(t, pool, table, first)
	require.Equal(t, "available", state, "release_to_back must restore state=available")
	require.Nil(t, tok)

	// All three rows are still in the table (ring buffer never deletes).
	require.Equal(t, 3, countItemsByState(t, pool, table, "available"))
	require.Equal(t, 0, countItemsByState(t, pool, table, "in_progress"))

	// Next claim must be "second" — "first" was just released_to_back so
	// its enqueued_at is now newer than "second"'s.
	got := acquireOnce(t, ctx, pool, s)
	require.Equal(t, second.String(), got.ClaimID,
		"after release_to_back of first, next claim must be second")

	// And the next-next is "third".
	got = acquireOnce(t, ctx, pool, s)
	require.Equal(t, third.String(), got.ClaimID)

	// Then "first" comes around again (still in the ring).
	releaseInTx(t, ctx, pool, s, second.String(), "release_to_back")
	releaseInTx(t, ctx, pool, s, third.String(), "release_to_back")
	got = acquireOnce(t, ctx, pool, s)
	require.Equal(t, first.String(), got.ClaimID, "ring buffer cycles back to first")
}
