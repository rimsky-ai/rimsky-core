// §19.1 — held claim, linear chain: a single terminal-leaf node
// resolves a held claim at commit. Mirrors the §11.5 worked example
// shape (claim-source → review terminal). The terminal's
// `claim_resolutions` entry maps to a `rimsky_claim_holders` row; on
// terminal commit the §5.6.4 algorithm fires the items-table reposition
// (last-released-wins; one holder = one resolution).
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres. Acquisition uses the real AcquireLock; the
// terminal uses ResolveOnTerminal which runs the §5.6.4 algorithm in
// the supplied tx.
package claim_stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

func TestClaimHoldLinearChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "linear_hold_items"
	createItemsTable(t, pool, table)
	// Ring-buffer defaults: on_commit=release_to_back. The held-claim's
	// terminal resolves with this disposition unless overridden.
	s := buildStore(t, pool, "linear_hold", table, "release_to_back", "release_to_head")

	// Seed one item; claim it. State is now in_progress.
	id := insertItem(t, pool, table, map[string]any{"area": "north"}, unsetTime())
	cr := acquireOnce(t, ctx, pool, s)
	require.Equal(t, id.String(), cr.ClaimID)
	state, _ := readItemState(t, pool, table, id)
	require.Equal(t, "in_progress", state)

	// Provision a (template, instance, terminal-node) triple. The
	// terminal here represents the "review" leaf in §11.5: it inherits
	// the holding subgraph and is responsible for resolving the claim.
	_, _, terminalNodeID := provisionTemplateInstanceNode(t, pool, "linear_chain")

	// Commit-time of the claim source inserts one rimsky_claim_holders
	// row per terminal-leaf identified by the §11.4 walk. For this
	// linear chain there is exactly one terminal.
	holderRowID := insertHolder(t, pool, id.String(), s.Name(),
		terminalNodeID, "release_to_back", "release_to_head")

	// Terminal commits → ResolveOnTerminal fires the resolution. With
	// one holder and zero deletes, the §5.6.4 last-released-wins branch
	// runs ReleaseClaimItem(release_to_back).
	resolveInTx(t, ctx, pool, s, id.String(), terminalNodeID, claimstorepg.TerminalCommit)

	// (a) holder row: state=completed, actual_action=release_to_back.
	hstate, action := readHolder(t, pool, holderRowID)
	require.Equal(t, "completed", hstate)
	require.Equal(t, "release_to_back", action)

	// (b) items-table row: state=available, claim_token cleared.
	itemState, tok := readItemState(t, pool, table, id)
	require.Equal(t, "available", itemState)
	require.Nil(t, tok)
}
