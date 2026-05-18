// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_acquire_named_locks.go — named-lock acquisition path. Split
// out of `runner_acquire.go` per the 2026-05-17 cold-read paydown
// (Item 4 / Tier 1) so each concern lives in a < 500-line file.

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
)

// takeNamedAdvisoryLocks walks the sorted spec slice and takes one
// advisory lock per NamedLockSpec.
func takeNamedAdvisoryLocks(ctx context.Context, args RunArgs, tx persistence.Tx, specs []any) error {
	for _, sp := range specs {
		named, ok := sp.(locks.NamedLockSpec)
		if !ok {
			continue
		}
		if err := args.AdvisoryLocker.TakeNamedLockInTx(ctx, tx, named.Name); err != nil {
			return fmt.Errorf("takeNamedAdvisoryLocks(%q): %w", named.Name, err)
		}
	}
	return nil
}

// acquireOneLock dispatches one spec to the right acquisition path and
// returns one of the three openResult flavors. NamedLockSpec acquisitions
// never report Unavailable (acquired or bail only). ClaimSpec
// acquisitions may report Unavailable when the producer's Open returns
// Available=false.
func acquireOneLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	sp any, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, openResult, error) {
	switch spec := sp.(type) {
	case locks.NamedLockSpec:
		al, ok, err := acquireNamedLock(ctx, args, tx, spec, cand, heartbeatInterval)
		if err != nil {
			return AcquiredLock{}, openResultBail, err
		}
		if !ok {
			return AcquiredLock{}, openResultBail, nil
		}
		return al, openResultAcquired, nil
	case locks.ClaimSpec:
		return acquireClaim(ctx, args, tx, spec, cand, heartbeatInterval, heldSubgraphs)
	}
	return AcquiredLock{}, openResultBail, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
}

// acquireNamedLock enforces the counter-semaphore limit then inserts
// the named lock-holder row. The per-name advisory lock has been
// taken upstream (takeNamedAdvisoryLocks); under that lock the
// CountByNamedLock + Insert pair is atomic against the limit.
//
// When the operator's NamedLocks config has no entry for this name,
// no limit is enforced (limit defaults to ∞). Templates referencing
// undeclared names should have failed validation at deploy time
// (control-api wires NamedLockDeclared unconditionally).
func acquireNamedLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.NamedLockSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
) (AcquiredLock, bool, error) {
	if cfg, ok := args.NamedLocks.Get(spec.Name); ok {
		count, err := args.ClaimHandles.CountByNamedLock(ctx, spec.Name, tx)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: CountByNamedLock(%q): %w", spec.Name, err)
		}
		if count >= cfg.Limit {
			return AcquiredLock{}, false, nil
		}
	}
	rowID := uuid.New()
	frameID := cand.FrameID
	dispatchID := cand.DispatchID
	nameCopy := spec.Name
	in := persistence.ClaimHandleInsertInput{
		ID:                 rowID,
		NodeRunID:          &dispatchID,
		LockKind:           persistence.LockKindNamed,
		LockName:           &nameCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          args.Clock.Now().Add(5 * heartbeatInterval),
		FrameID:            &frameID,
		// Named locks are never held past active terminal; they release
		// at the node-run's active-phase terminal.
		IsHeld: false,
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: Insert: %w", err)
	}
	return AcquiredLock{
		Spec:          spec,
		ClaimHandleID: rowID,
	}, true, nil
}
