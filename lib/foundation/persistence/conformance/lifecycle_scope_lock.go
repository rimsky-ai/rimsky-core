// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
		serviceName = "lifecycle-lock-service"
		scopeID     = "lifecycle-lock-scope-1"
	)
	scopeKind := persistence.LifecycleScopeRunScope

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
			ClaimProducerName: serviceName,
			ScopeKind:         scopeKind,
			ScopeID:           scopeID,
			Event:             "EventRunScopeTerminal",
			Payload:           []byte(`{}`),
		}, tx)
	}); err != nil {
		t.Fatalf("stage the run-scope terminal every racer competes to deliver: %v", err)
	}
	var staged []persistence.LifecycleOutboxRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := store.LifecycleOutbox().ListPendingForScope(ctx, scopeKind, scopeID, tx)
		staged = rows
		return err
	}); err != nil {
		t.Fatalf("read the staged row back: %v", err)
	}
	if len(staged) != 1 {
		t.Fatalf("staging left %d rows, want 1", len(staged))
	}
	seq := staged[0].Seq

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
				row, err := store.LifecycleOutbox().GetBySeq(ctx, seq, tx)
				if err != nil {
					return err
				}
				if row == nil {
					return nil
				}
				deliveries.Add(1)
				return store.LifecycleOutbox().DeleteBySeq(ctx, seq, tx)
			})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: delivery section tx: %v", i, err)
		}
	}
	if got := deliveries.Load(); got != 1 {
		t.Fatalf("deliveries = %d, want exactly 1: the lifecycle scope lock must serialize the "+
			"[re-read row, deliver, delete row] section across concurrent transactions so racing "+
			"drains converge to a single delivery", got)
	}
}
