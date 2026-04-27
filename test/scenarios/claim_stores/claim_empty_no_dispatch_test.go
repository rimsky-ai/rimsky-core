// §19.1 — empty claim store yields no dispatch: a claim-store with no
// available rows must report HasClaimableItem=false and AcquireLock
// surfaces an empty ClaimResult so the supervisor's atomic acquisition
// transaction rolls back without claiming a dispatch row.
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres.
package claim_stores

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClaimEmptyNoDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "empty_pool_items"
	createItemsTable(t, pool, table)
	s := buildStore(t, pool, "empty_pool", table, "delete", "release_to_head")

	// (a) HasClaimableItem on empty pool: false. The supervisor's §13.2
	// pre-screen consults this; "false" causes the dispatch row to be
	// skipped entirely (no atomic acquisition tx is opened).
	has, err := s.HasClaimableItem(ctx, nil)
	require.NoError(t, err)
	require.False(t, has, "empty pool must report HasClaimableItem=false")

	// (b) Even if a confused supervisor races past the pre-screen, the
	// inside-tx AcquireLock returns an empty ClaimResult — the supervisor
	// rolls back and the dispatch row stays unclaimed.
	got := acquireOnce(t, ctx, pool, s)
	require.Empty(t, got.ClaimID,
		"AcquireLock on empty pool must return empty ClaimResult so the outer tx rolls back")
	require.Nil(t, got.Payload)

	// (c) After flipping a row to in_progress out-of-band, HasClaimableItem
	// is still false — only state='available' counts.
	id := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO `+table+` (item_id, payload, state, claim_token, claimed_at)
		 VALUES ($1, '{}'::jsonb, 'in_progress', gen_random_uuid(), now())`,
		id,
	)
	require.NoError(t, err)
	has, err = s.HasClaimableItem(ctx, nil)
	require.NoError(t, err)
	require.False(t, has, "in-progress-only pool must still report HasClaimableItem=false")
}
