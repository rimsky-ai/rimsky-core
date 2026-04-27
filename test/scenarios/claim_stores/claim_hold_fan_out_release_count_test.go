// §19.1 — held claim, fan-out, release count: two terminal-leaf nodes
// both resolve with release (no delete in the mix). The actual store-side
// release fires only when the count of active holders → 0. The first
// resolution observes ACTIVE_COUNT > 0 and skips the items-table
// reposition; the second sees ACTIVE_COUNT = 0 (and DELETE_COUNT = 0)
// and fires the configured release action.
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

func TestClaimHoldFanOutReleaseCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "fan_relcount_items"
	createItemsTable(t, pool, table)
	s := buildStore(t, pool, "fan_relcount", table, "release_to_back", "release_to_head")

	id := insertItem(t, pool, table, map[string]any{"k": "v"}, unsetTime())
	cr := acquireOnce(t, ctx, pool, s)
	require.Equal(t, id.String(), cr.ClaimID)

	_, instID, nodeA := provisionTemplateInstanceNode(t, pool, "relcount-A")
	nodeB := insertSiblingNode(t, pool, instID, "leaf-relcount-B")

	// Both terminals release; both use release_to_back as their
	// on_commit. The §5.6.4 algorithm should fire the items-table
	// reposition exactly once, on the second (last) resolution.
	hA := insertHolder(t, pool, id.String(), s.Name(), nodeA, "release_to_back", "release_to_head")
	hB := insertHolder(t, pool, id.String(), s.Name(), nodeB, "release_to_back", "release_to_head")

	// First resolution (nodeA): ACTIVE_COUNT == 1 (nodeB still active),
	// so ReleaseClaimItem is NOT fired — the items-table row stays
	// in_progress.
	resolveInTx(t, ctx, pool, s, id.String(), nodeA, claimstorepg.TerminalCommit)
	state, action := readHolder(t, pool, hA)
	require.Equal(t, "completed", state)
	require.Equal(t, "release_to_back", action)

	itemState, tok := readItemState(t, pool, table, id)
	require.Equal(t, "in_progress", itemState,
		"items-table must remain in_progress until the last release resolves")
	require.NotNil(t, tok, "claim_token must persist on the in_progress row")

	// Second resolution (nodeB): ACTIVE_COUNT == 0, DELETE_COUNT == 0,
	// so ReleaseClaimItem(release_to_back) fires inside the same tx as
	// the holder-row update. Items-table row goes back to 'available'.
	resolveInTx(t, ctx, pool, s, id.String(), nodeB, claimstorepg.TerminalCommit)
	state, action = readHolder(t, pool, hB)
	require.Equal(t, "completed", state)
	require.Equal(t, "release_to_back", action)

	itemState2, tok2 := readItemState(t, pool, table, id)
	require.Equal(t, "available", itemState2,
		"after both terminals release, the items-table row must return to available")
	require.Nil(t, tok2)
}
