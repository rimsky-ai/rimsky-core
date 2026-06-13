// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// advisory_locker_test.go pins the cross-process exclusion of the
// flock-based scheduler-tick and migration locks. The load-bearing
// property: exclusion must hold across OS processes sharing one
// database file, not merely across goroutines. flock(2) contends per
// open file description and every acquisition opens its own fd, so two
// independent locker instances inside one test process contend exactly
// the way two processes do — each TrySchedulerTick / migration
// acquisition locks a fresh fd on the same lock-file inode.

package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestTrySchedulerTick_ExcludesAcrossLockerInstances simulates two
// processes sharing one database path with two independent locker
// instances: the first holds the tick lock, the second must observe
// held=false; after release the second acquires.
func TestTrySchedulerTick_ExcludesAcrossLockerInstances(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	lockerA := newAdvisoryLocker(dbPath)
	lockerB := newAdvisoryLocker(dbPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	heldA, releaseA, err := lockerA.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("locker A TrySchedulerTick: %v", err)
	}
	if !heldA {
		t.Fatal("locker A TrySchedulerTick returned held=false on a fresh path")
	}

	heldB, releaseB, err := lockerB.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("locker B TrySchedulerTick: %v", err)
	}
	if heldB {
		releaseB()
		t.Fatal("locker B acquired the tick lock while locker A still held it — cross-instance exclusion broken")
	}
	if releaseB != nil {
		t.Fatal("locker B got a non-nil release fn with held=false")
	}

	releaseA()

	heldB2, releaseB2, err := lockerB.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("locker B TrySchedulerTick after release: %v", err)
	}
	if !heldB2 {
		t.Fatal("locker B could not acquire the tick lock after locker A released it")
	}
	releaseB2()
}

// TestAcquireMigrationLock_BlocksAcrossLockerInstances pins the blocking
// contract across two independent locker instances on one path: B's
// acquisition does not return until A releases.
func TestAcquireMigrationLock_BlocksAcrossLockerInstances(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	lockerA := newAdvisoryLocker(dbPath)
	lockerB := newAdvisoryLocker(dbPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	releaseA, err := lockerA.AcquireMigrationLock(ctx)
	if err != nil {
		t.Fatalf("locker A AcquireMigrationLock: %v", err)
	}

	acquired := make(chan func() error, 1)
	errCh := make(chan error, 1)
	go func() {
		rel, err := lockerB.AcquireMigrationLock(ctx)
		if err != nil {
			errCh <- err
			return
		}
		acquired <- rel
	}()

	select {
	case <-acquired:
		t.Fatal("locker B acquired the migration lock while locker A still held it — cross-instance exclusion broken")
	case err := <-errCh:
		t.Fatalf("locker B AcquireMigrationLock: %v", err)
	case <-time.After(150 * time.Millisecond):
		// expected: B is still blocked.
	}

	if err := releaseA(); err != nil {
		t.Fatalf("locker A release: %v", err)
	}

	select {
	case releaseB := <-acquired:
		if err := releaseB(); err != nil {
			t.Fatalf("locker B release: %v", err)
		}
	case err := <-errCh:
		t.Fatalf("locker B AcquireMigrationLock after release: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("locker B never acquired the migration lock after locker A released it")
	}
}

// TestAcquireMigrationLock_HonorsContextCancel pins that a blocked
// acquisition returns when its context is cancelled instead of waiting
// forever on the other holder.
func TestAcquireMigrationLock_HonorsContextCancel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	lockerA := newAdvisoryLocker(dbPath)
	lockerB := newAdvisoryLocker(dbPath)

	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer acquireCancel()
	releaseA, err := lockerA.AcquireMigrationLock(acquireCtx)
	if err != nil {
		t.Fatalf("locker A AcquireMigrationLock: %v", err)
	}
	defer func() { _ = releaseA() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := lockerB.AcquireMigrationLock(ctx); err == nil {
		t.Fatal("locker B AcquireMigrationLock returned nil error while locker A held the lock and ctx expired")
	}
}
