// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// auto_terminal.go — HeldClaimAutoTerminalSerialization conformance area.
//
// Inv 13: held-claim resolution is auto-terminal, single, and aggregate-
// outcome-driven. Two concurrent Transactions calling
// ClaimHandles.LockForUpdate(ctx, id, tx) must serialise — the second
// blocks until the first commits.
package conformance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testHeldClaimAutoTerminalSerialization(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	lockHolderID := uuid.New()
	supID := "autoterminal-supervisor"
	lockName := "autoterminal-lock"

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: supID,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
		}, tx)
	}); err != nil {
		t.Fatalf("seed lock-holder: %v", err)
	}

	// Two concurrent transactions both call LockForUpdate. The first
	// holds the row "lock" (Postgres FOR UPDATE; SQLite BEGIN IMMEDIATE
	// writer slot) for ~200ms before committing. The second's start
	// timestamp must be after the first's commit timestamp.
	var (
		firstHoldStart    int64
		firstCommitDone   int64
		secondLockGrabbed int64
	)
	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: grab lock, sleep, commit.
	go func() {
		defer wg.Done()
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			row, err := store.ClaimHandles().LockForUpdate(ctx, lockHolderID, tx)
			if err != nil {
				return err
			}
			if row == nil {
				t.Errorf("LockForUpdate #1 returned nil row")
				return nil
			}
			atomic.StoreInt64(&firstHoldStart, time.Now().UnixNano())
			time.Sleep(200 * time.Millisecond)
			atomic.StoreInt64(&firstCommitDone, time.Now().UnixNano())
			return nil
		})
		if err != nil {
			t.Errorf("tx #1: %v", err)
		}
	}()

	// Give goroutine 1 a head start so it grabs the lock first.
	time.Sleep(50 * time.Millisecond)

	go func() {
		defer wg.Done()
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			row, err := store.ClaimHandles().LockForUpdate(ctx, lockHolderID, tx)
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

	t1 := atomic.LoadInt64(&firstHoldStart)
	c1 := atomic.LoadInt64(&firstCommitDone)
	t2 := atomic.LoadInt64(&secondLockGrabbed)
	if t1 == 0 || c1 == 0 || t2 == 0 {
		t.Fatalf("missing timestamps: t1=%d c1=%d t2=%d", t1, c1, t2)
	}
	// The second tx must not grab the lock before the first commits.
	if t2 < c1 {
		t.Fatalf("LockForUpdate did not serialise: tx2 grabbed lock at %d, before tx1 committed at %d",
			t2, c1)
	}
}
