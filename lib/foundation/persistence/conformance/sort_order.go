// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testSortOrderCoordination(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	coord := d.AdvisoryLocker()
	if coord == nil {
		t.Fatalf("driver.Coordinator() returned nil")
	}

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
					if err := coord.TakeClaimScopeLockInTx(ctx, tx, storeName, r); err != nil {
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
