// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testClaimHandleLockForUpdateSerializesConcurrentTx(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	claimHandleID := uuid.New()
	supID := "lock-for-update-supervisor"
	lockName := "lock-for-update-lock"

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim-handle: %v", err)
	}

	var (
		firstCommitDone   int64
		secondLockGrabbed int64
	)
	acquired := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			row, err := store.ClaimHandles().LockForUpdate(ctx, claimHandleID, tx)
			if err != nil {
				return err
			}
			if row == nil {
				t.Errorf("LockForUpdate #1 returned nil row")
				return nil
			}
			close(acquired)
			atomic.StoreInt64(&firstCommitDone, time.Now().UnixNano())
			return nil
		})
		if err != nil {
			t.Errorf("tx #1: %v", err)
		}
	}()

	<-acquired

	go func() {
		defer wg.Done()
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			row, err := store.ClaimHandles().LockForUpdate(ctx, claimHandleID, tx)
			if err != nil {
				return err
			}
			if row == nil {
				t.Errorf("LockForUpdate #2 returned nil row")
				return nil
			}
			atomic.StoreInt64(&secondLockGrabbed, time.Now().UnixNano())
			return nil
		})
		if err != nil {
			t.Errorf("tx #2: %v", err)
		}
	}()

	wg.Wait()

	c1 := atomic.LoadInt64(&firstCommitDone)
	t2 := atomic.LoadInt64(&secondLockGrabbed)
	if c1 == 0 || t2 == 0 {
		t.Fatalf("missing timestamps: c1=%d t2=%d", c1, t2)
	}
	if t2 < c1 {
		t.Fatalf("LockForUpdate did not serialise: tx2 grabbed lock at %d, before tx1 committed at %d",
			t2, c1)
	}
}
