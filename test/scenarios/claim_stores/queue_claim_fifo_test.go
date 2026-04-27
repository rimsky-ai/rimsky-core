// §19.1 — queue claim FIFO selection: a claim_store with on_commit=delete
// hands items out in enqueued_at order, and the payload arrives at the
// caller intact.
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres. Items are seeded with strictly increasing
// enqueued_at; three sequential acquisitions must surface them in the
// same order.
package claim_stores

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueueClaimFIFO(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "fifo_queue_items"
	createItemsTable(t, pool, table)
	s := buildStore(t, pool, "fifo_queue", table, "delete", "release_to_head")

	// Three items with strictly increasing enqueued_at (1ms apart) so
	// the FIFO order is observable independent of insert order.
	base := time.Now().UTC()
	id0 := insertItem(t, pool, table,
		map[string]any{"seq": 0, "topic": "alpha"}, base)
	id1 := insertItem(t, pool, table,
		map[string]any{"seq": 1, "topic": "beta"}, base.Add(time.Millisecond))
	id2 := insertItem(t, pool, table,
		map[string]any{"seq": 2, "topic": "gamma"}, base.Add(2*time.Millisecond))

	want := []struct {
		id    string
		topic string
	}{
		{id0.String(), "alpha"},
		{id1.String(), "beta"},
		{id2.String(), "gamma"},
	}
	for i, w := range want {
		got := acquireOnce(t, ctx, pool, s)
		require.Equal(t, w.id, got.ClaimID, "acquisition %d: FIFO order broken", i)
		payload, ok := got.Payload.(map[string]any)
		require.True(t, ok, "acquisition %d: payload type %T", i, got.Payload)
		require.Equal(t, w.topic, payload["topic"], "acquisition %d: payload not carried", i)
		// items-table row should now be in_progress.
		state, _ := readItemState(t, pool, table, mustParseUUID(t, w.id))
		require.Equal(t, "in_progress", state)
	}

	// Pool drained; fourth claim returns the empty ClaimResult.
	got := acquireOnce(t, ctx, pool, s)
	require.Empty(t, got.ClaimID, "fourth claim must return empty ClaimResult on drained pool")
}
