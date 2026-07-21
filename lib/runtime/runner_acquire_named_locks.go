// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func takeNamedAdvisoryLocks(ctx context.Context, args RunArgs, specs []any, tx persistence.Tx) error {
	for _, sp := range specs {
		named, ok := sp.(locks.NamedLockSpec)
		if !ok {
			continue
		}
		if err := args.AdvisoryLocker.TakeNamedLock(ctx, named.Name, tx); err != nil {
			return fmt.Errorf("takeNamedAdvisoryLocks(%q): %w", named.Name, err)
		}
	}
	return nil
}

func acquireOneLock(
	ctx context.Context, args RunArgs, instanceID shared.UUID, sp any, cand persistence.Candidate, livenessInterval time.Duration, heldSubgraphs []node.HoldingSubgraph, acquired []AcquiredLock, tx persistence.Tx,
) (AcquiredLock, openResult, error) {
	switch spec := sp.(type) {
	case locks.NamedLockSpec:
		// @concept: parked-state
		if al, reused, err := reuseHeldNamedLock(ctx, args, spec, cand, acquired, livenessInterval, tx); err != nil {
			return AcquiredLock{}, openResultBail, err
		} else if reused {
			return al, openResultAcquired, nil
		}
		al, ok, err := acquireNamedLock(ctx, args, spec, cand, livenessInterval, tx)
		if err != nil {
			return AcquiredLock{}, openResultBail, err
		}
		if !ok {
			return AcquiredLock{}, openResultBail, nil
		}
		return al, openResultAcquired, nil
	case claimproducer.ClaimSpec:
		return acquireClaim(ctx, args, instanceID, spec, cand, livenessInterval, heldSubgraphs, acquired, tx)
	}
	return AcquiredLock{}, openResultBail, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
}

// @concept: named-lock
func acquireNamedLock(
	ctx context.Context, args RunArgs, spec locks.NamedLockSpec, cand persistence.Candidate, livenessInterval time.Duration, tx persistence.Tx,
) (AcquiredLock, bool, error) {
	if cfg, ok := args.NamedLocks.Get(spec.Name); ok {
		count, err := args.ClaimHandles.CountByNamedLock(ctx, spec.Name, tx)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: CountByNamedLock(%q): %w", spec.Name, err)
		}
		if count >= cfg.Limit {
			metricsOf(args).IncNamedLockAcquisition(namedLockMetricLabel(spec), "unavailable")
			return AcquiredLock{}, false, nil
		}
	}
	rowID := uuid.New()
	frameID := cand.FrameID
	nodeRunID := cand.NodeRunID
	nameCopy := spec.Name
	in := persistence.ClaimHandleInsertInput{
		ID:                 rowID,
		NodeRunID:          &nodeRunID,
		LockKind:           persistence.LockKindNamed,
		LockName:           &nameCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          claimExpiryFromLiveness(args.Clock.Now(), livenessInterval),
		FrameID:            &frameID,
		IsHeld:             false,
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: Insert: %w", err)
	}
	// @story: named-lock-metric
	return AcquiredLock{
		Spec:          spec,
		ClaimHandleID: rowID,
	}, true, nil
}

func namedLockMetricLabel(spec locks.NamedLockSpec) string {
	if spec.TemplateName != "" {
		return spec.TemplateName
	}
	return spec.Name
}
