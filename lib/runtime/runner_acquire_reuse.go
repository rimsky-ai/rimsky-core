// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: parked-state
// @concept: claim-handle

package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	fspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func lockAlreadyReused(acquired []AcquiredLock, id shared.UUID) bool {
	for i := range acquired {
		if acquired[i].ClaimHandleID == id {
			return true
		}
	}
	return false
}

func renewReusedRunExpiry(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	nodeRunID shared.UUID, livenessInterval time.Duration,
) error {
	return args.ClaimHandles.RenewExpiryForHolderRun(ctx, nodeRunID, args.Clock.Now().Add(5*livenessInterval), tx)
}

func reuseHeldNamedLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.NamedLockSpec, cand persistence.Candidate,
	acquired []AcquiredLock, livenessInterval time.Duration,
) (AcquiredLock, bool, error) {
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, cand.NodeRunID, tx)
	if err != nil {
		return AcquiredLock{}, false, err
	}
	for i := range rows {
		row := rows[i]
		if row.State != fspec.ClaimHandleStateActive || row.LockKind != persistence.LockKindNamed {
			continue
		}
		if row.LockName == nil || *row.LockName != spec.Name {
			continue
		}
		if lockAlreadyReused(acquired, row.ID) {
			continue
		}
		if err := renewReusedRunExpiry(ctx, args, tx, cand.NodeRunID, livenessInterval); err != nil {
			return AcquiredLock{}, false, err
		}
		return AcquiredLock{Spec: spec, ClaimHandleID: row.ID}, true, nil
	}
	return AcquiredLock{}, false, nil
}

func reuseHeldRunClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec claimproducer.ClaimSpec, cand persistence.Candidate,
	scopeInitial []byte, s claimproducer.ClaimProducer,
	acquired []AcquiredLock, livenessInterval time.Duration,
) (AcquiredLock, bool, error) {
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, cand.NodeRunID, tx)
	if err != nil {
		return AcquiredLock{}, false, err
	}
	var (
		candidates []persistence.ClaimHandleRow
		exact      *persistence.ClaimHandleRow
	)
	for i := range rows {
		row := rows[i]
		if row.State != fspec.ClaimHandleStateActive || row.LockKind != persistence.LockKindScope {
			continue
		}
		if row.ParentClaimHandleID != nil || row.ProducerName == nil || *row.ProducerName != spec.ProducerName {
			continue
		}
		if lockAlreadyReused(acquired, row.ID) {
			continue
		}
		candidates = append(candidates, row)
		if string(row.ClaimScopeData) == string(scopeInitial) {
			r := row
			exact = &r
			break
		}
	}
	chosen := exact
	if chosen == nil {
		match, err := pickScopeMatch(ctx, s, scopeInitial, candidates)
		if err != nil {
			return AcquiredLock{}, false, err
		}
		chosen = match
	}
	if chosen == nil {
		return AcquiredLock{}, false, nil
	}
	if err := renewReusedRunExpiry(ctx, args, tx, cand.NodeRunID, livenessInterval); err != nil {
		return AcquiredLock{}, false, err
	}
	return AcquiredLock{
		Spec:          spec,
		ClaimHandleID: chosen.ID,
		ClaimResult: claimproducer.ClaimResult{
			ClaimScope:             json.RawMessage(chosen.ClaimScopeData),
			Address:                json.RawMessage(chosen.Address),
			Payload:                json.RawMessage(chosen.Payload),
			RealizedWriteSemantics: claimproducer.WriteSemantics(chosen.RealizedWriteSemantics),
		},
		Producer: s,
		Alias:    spec.Alias,
		IsHeld:   chosen.IsHeld,
	}, true, nil
}

func pickScopeMatch(
	ctx context.Context, s claimproducer.ClaimProducer,
	scopeInitial []byte, candidates []persistence.ClaimHandleRow,
) (*persistence.ClaimHandleRow, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		conflicts, err := scopesConflict(ctx, s, caps, scopeInitial, candidates[i].ClaimScopeData)
		if err != nil {
			return nil, err
		}
		if conflicts {
			return &candidates[i], nil
		}
	}
	return nil, nil
}
