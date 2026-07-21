// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type namedLockCapacityClaimHandles struct {
	persistence.ClaimHandleTable
	count    int
	inserted []persistence.ClaimHandleInsertInput
}

func (f *namedLockCapacityClaimHandles) CountByNamedLock(_ context.Context, _ string, _ persistence.Tx) (int, error) {
	return f.count, nil
}

func (f *namedLockCapacityClaimHandles) Insert(_ context.Context, in persistence.ClaimHandleInsertInput, _ persistence.Tx) error {
	f.inserted = append(f.inserted, in)
	return nil
}

// @concept: named-lock
func TestAcquireNamedLock_AtCapacityRejectsWithoutInsert(t *testing.T) {
	fake := &namedLockCapacityClaimHandles{count: 2}
	args := RunArgs{
		ClaimHandles: fake,
		NamedLocks:   locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{"budget": {Limit: 2}}},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-1",
	}
	cand := persistence.Candidate{NodeRunID: shared.UUID{}, NodeID: shared.UUID{}}

	_, ok, err := acquireNamedLock(context.Background(), args, locks.NamedLockSpec{Name: "budget"}, cand, time.Second, nil)
	if err != nil {
		t.Fatalf("acquireNamedLock: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("acquireNamedLock: ok = true, want false when count(%d) >= limit(%d)", fake.count, 2)
	}
	if len(fake.inserted) != 0 {
		t.Fatalf("acquireNamedLock: Insert called %d times, want 0 when at capacity", len(fake.inserted))
	}
}

func TestAcquireNamedLock_UnderCapacityAcquires(t *testing.T) {
	fake := &namedLockCapacityClaimHandles{count: 1}
	args := RunArgs{
		ClaimHandles: fake,
		NamedLocks:   locks.NamedLocksConfig{Locks: map[string]locks.NamedLockConfig{"budget": {Limit: 2}}},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-1",
	}
	cand := persistence.Candidate{NodeRunID: shared.UUID{}, NodeID: shared.UUID{}}

	_, ok, err := acquireNamedLock(context.Background(), args, locks.NamedLockSpec{Name: "budget"}, cand, time.Second, nil)
	if err != nil {
		t.Fatalf("acquireNamedLock: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("acquireNamedLock: ok = false, want true when count(%d) < limit(%d)", fake.count, 2)
	}
	if len(fake.inserted) != 1 {
		t.Fatalf("acquireNamedLock: Insert called %d times, want 1 when under capacity", len(fake.inserted))
	}
}

func TestAcquireNamedLock_UnconfiguredNameIsUnlimited(t *testing.T) {
	fake := &namedLockCapacityClaimHandles{count: 1_000_000}
	args := RunArgs{
		ClaimHandles: fake,
		NamedLocks:   locks.NamedLocksConfig{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-1",
	}
	cand := persistence.Candidate{NodeRunID: shared.UUID{}, NodeID: shared.UUID{}}

	_, ok, err := acquireNamedLock(context.Background(), args, locks.NamedLockSpec{Name: "unbounded"}, cand, time.Second, nil)
	if err != nil {
		t.Fatalf("acquireNamedLock: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("acquireNamedLock: ok = false, want true for a name with no configured limit")
	}
}
