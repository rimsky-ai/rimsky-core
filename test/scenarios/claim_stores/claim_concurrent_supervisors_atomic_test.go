// §19.1 — N concurrent supervisors race for the same single item; only
// one wins. Verifies the §13.3 atomic-acquisition guarantee for claim
// stores: the items-table flip uses `SELECT ... FOR UPDATE SKIP LOCKED`
// (`core/store/claimstorepg/acquire.go`), so concurrent transactions
// cannot both read the same row.
//
// Drives the real `core/store/claimstorepg/` Store against a
// testcontainers postgres. We seed a single item, fire N goroutines that
// each open their own tx and call AcquireLock; exactly one returns a
// non-empty ClaimResult.
package claim_stores

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/store"
)

func TestClaimConcurrentSupervisorsAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const table = "concurrent_items"
	createItemsTable(t, pool, table)
	s := buildStore(t, pool, "concurrent", table, "delete", "release_to_head")

	id := insertItem(t, pool, table, map[string]any{"only": true} /*enqAt*/, unsetTime())

	// N=8 simulates eight independent supervisor goroutines. Each opens
	// its own tx and races AcquireLock against the single item.
	const n = 8
	var (
		wg      sync.WaitGroup
		startWG sync.WaitGroup
		mu      sync.Mutex
		results []string // captured ClaimIDs across all goroutines
	)
	startWG.Add(1) // released after every goroutine has spun up
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			startWG.Wait()
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			_, cr, err := s.AcquireLock(store.WithTx(ctx, tx), store.ClaimLockSpec{StoreName: s.Name()})
			require.NoError(t, err)
			// Commit only when the goroutine actually claimed the row.
			// Empty ClaimID means SKIP LOCKED skipped past the only row;
			// rolling back doesn't change the items-table state.
			if cr.ClaimID != "" {
				require.NoError(t, tx.Commit(ctx))
				mu.Lock()
				results = append(results, cr.ClaimID)
				mu.Unlock()
			}
		}()
	}
	startWG.Done()
	wg.Wait()

	// Exactly one supervisor saw the row.
	require.Len(t, results, 1, "expected exactly one winner, got %v", results)
	require.Equal(t, id.String(), results[0])

	// Items-table reflects a single in_progress row (the winner's flip)
	// — the other goroutines could not have produced any state change.
	state, _ := readItemState(t, pool, table, id)
	require.Equal(t, "in_progress", state)
}
