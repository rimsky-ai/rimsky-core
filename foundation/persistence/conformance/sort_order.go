// sort_order.go — SortOrderCoordination conformance area.
//
// Inv 3, inv 10: when multiple goroutines take the same set of locks in
// sorted order, no deadlock occurs.
package conformance

import (
	"context"
	"sync"
	"testing"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testSortOrderCoordination(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	store := d.Store()
	coord := d.AdvisoryLocker()
	if coord == nil {
		t.Fatalf("driver.Coordinator() returned nil")
	}

	// Three named locks, lexically sorted.
	names := []string{"lock-a", "lock-b", "lock-c"}

	const iterations = 5
	var wg sync.WaitGroup
	wg.Add(2)
	run := func(label string) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				for _, n := range names {
					if err := coord.TakeNamedLockInTx(ctx, tx, n); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Errorf("%s: named-lock tx %d: %v", label, i, err)
				return
			}
		}
	}
	go run("A")
	go run("B")
	wg.Wait()

	// Scope locks: same drill.
	storeName := "scope-sort-store"
	scopes := [][]byte{
		[]byte(`{"r":1}`),
		[]byte(`{"r":2}`),
		[]byte(`{"r":3}`),
	}
	wg.Add(2)
	runScope := func(label string) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				for _, r := range scopes {
					if err := coord.TakeScopeLockInTx(ctx, tx, storeName, r); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Errorf("%s: scope-lock tx %d: %v", label, i, err)
				return
			}
		}
	}
	go runScope("A")
	go runScope("B")
	wg.Wait()
}
