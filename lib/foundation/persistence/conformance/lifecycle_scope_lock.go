// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: advisory-lock
// @concept: lifecycle-subscriber
func testLifecycleScopeLockSerializesFanOutSection(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	coord := d.AdvisoryLocker()
	if coord == nil {
		t.Fatalf("driver.AdvisoryLocker() returned nil")
	}

	const (
		peerName = "lifecycle-lock-peer"
		scopeID  = "lifecycle-lock-scope-1"
	)
	scopeKind := persistence.LifecycleIdempotencyScopeRunScope

	const racers = 8
	var deliveries atomic.Int32
	var wg sync.WaitGroup
	wg.Add(racers)
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if err := coord.TakeLifecycleScopeLock(ctx, scopeKind, scopeID, tx); err != nil {
					return err
				}
				row, err := store.LifecycleIdempotency().Get(ctx, peerName, scopeKind, scopeID, tx)
				if err != nil {
					return err
				}
				if row != nil && row.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
					return nil
				}
				deliveries.Add(1)
				return store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
					ClaimProducerName: peerName,
					ScopeKind:         scopeKind,
					ScopeID:           scopeID,
					State:             persistence.LifecycleIdempotencyStateRunScopeTerminal,
				}, tx)
			})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: fan-out section tx: %v", i, err)
		}
	}
	if got := deliveries.Load(); got != 1 {
		t.Fatalf("deliveries = %d, want exactly 1: the lifecycle scope lock must serialize the "+
			"[check row, deliver, mark row] section across concurrent transactions so racing "+
			"fan-outs for one scope converge to a single delivery", got)
	}
}
