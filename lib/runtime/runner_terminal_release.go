// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func releaseLocksInTx(
	ctx context.Context, args RunArgs, acq *acquisition, success bool, retainLinkedSubClaims bool, tx persistence.Tx,
) (postCommitFn, error) {
	var post postCommitFn
	for _, lk := range acq.Locks {
		pc, err := releaseAcquiredLock(ctx, args, acq, lk, success, tx)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, pc)
	}
	if !retainLinkedSubClaims {
		pc, err := resolveLinkedSubClaimsInTx(ctx, args, acq, success, tx)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, pc)
	}
	pc, err := releaseInheritedClaimsInTx(ctx, args, acq, success, tx)
	if err != nil {
		return nil, err
	}
	post = chainPostCommit(post, pc)
	return post, nil
}

// @concept: claim-tree
// @concept: fan-out
func resolveLinkedSubClaimsInTx(
	ctx context.Context, args RunArgs, acq *acquisition, success bool, tx persistence.Tx,
) (postCommitFn, error) {
	if args.ClaimHandles == nil {
		return nil, nil
	}
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, acq.NodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("resolveLinkedSubClaims: ListByNodeRun: %w", err)
	}
	released := make(map[shared.UUID]bool, len(acq.Locks))
	for _, lk := range acq.Locks {
		released[lk.ClaimHandleID] = true
	}
	var post postCommitFn
	for i := range rows {
		row := rows[i]
		if row.ParentClaimHandleID == nil || released[row.ID] {
			continue
		}
		if row.IsHeld {
			continue
		}
		locked, err := args.ClaimHandles.LockForUpdate(ctx, row.ID, tx)
		if err != nil {
			return nil, fmt.Errorf("resolveLinkedSubClaims: LockForUpdate %s: %w", row.ID, err)
		}
		if locked == nil || locked.State != spec.ClaimHandleStateActive {
			continue
		}
		row = *locked
		producerName := ""
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		producer, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, producerName, "", tx)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolveLinkedSubClaims: resolving producer %q for sub-claim %s: %w", producerName, row.ID, resolveErr)
		}
		if !ok {
			return nil, fmt.Errorf("resolveLinkedSubClaims: unknown producer %q for sub-claim %s", producerName, row.ID)
		}
		outcome := OutcomeCommit
		if !success {
			outcome = OutcomeAbandon
		}
		hint := ClaimLineageHint{
			InstanceID:   acq.InstanceID,
			FrameID:      acq.FrameID,
			NodeRunID:    acq.NodeRunID,
			NodeID:       acq.NodeID,
			ProducerName: producerName,
			VersionID:    row.VersionID,
		}
		pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
			ClaimHandleID:       row.ID,
			SupervisorID:        args.SupervisorID,
			Source:              ActiveTerminal,
			Outcome:             outcome,
			Producer:            producer,
			Scope:               []byte(row.ClaimScopeData),
			Address:             []byte(row.Address),
			LeaseToken:          row.ProducerLeaseToken,
			Lifetime:            row.Lifetime,
			CandidateHandle:     row.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: row.ParentClaimHandleID,
		}, tx)
		if err != nil {
			return nil, fmt.Errorf("resolveLinkedSubClaims: sub-claim %s: %w", row.ID, err)
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}

func releaseAcquiredLock(
	ctx context.Context, args RunArgs, acq *acquisition, lk AcquiredLock, success bool, tx persistence.Tx,
) (postCommitFn, error) {
	switch sp := lk.Spec.(type) {
	// @concept: named-lock
	case locks.NamedLockSpec:
		_ = sp
		if err := args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
			return nil, fmt.Errorf("releaseAcquiredLock: named Delete: %w", err)
		}
		return nil, emitLockReleased(ctx, args, acq, lk, releaseActionString(success), tx)
	case claimproducer.ClaimSpec:
		return releaseClaim(ctx, args, acq, lk, sp, success, tx)
	}
	return nil, fmt.Errorf("releaseAcquiredLock: unknown spec %T", lk.Spec)
}

func releaseClaim(
	ctx context.Context, args RunArgs, acq *acquisition, lk AcquiredLock, claimSpec claimproducer.ClaimSpec, success bool, tx persistence.Tx,
) (postCommitFn, error) {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, claimSpec.Alias)
	if held {
		if err := markClaimHolderForRun(ctx, args, lk.ClaimHandleID, acq.NodeRunID, success, tx); err != nil {
			return nil, err
		}
		if !success {
			if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
				return nil, fmt.Errorf("releaseClaim: fail inheritors: %w", err)
			}
		}
		pc, err := CheckAndFireResolution(ctx, args, lk.ClaimHandleID, tx)
		if err != nil {
			return nil, err
		}
		if err := emitLockReleased(ctx, args, acq, lk, "held_marked", tx); err != nil {
			return nil, err
		}
		return pc, nil
	}
	row, err := args.ClaimHandles.LockForUpdate(ctx, lk.ClaimHandleID, tx)
	if err != nil {
		return nil, fmt.Errorf("releaseClaim: load scope/address: %w", err)
	}
	if row != nil && row.State != spec.ClaimHandleStateActive {
		if args.Logger != nil {
			args.Logger.Warn("releaseClaim: claim handle already resolved by a concurrent terminal path; skipping duplicate producer verb",
				"claim_handle_id", lk.ClaimHandleID.String(), "state", string(row.State))
		}
		return nil, emitLockReleased(ctx, args, acq, lk, releaseActionString(success), tx)
	}
	var (
		scope   []byte
		address []byte
	)
	if row != nil {
		scope = []byte(row.ClaimScopeData)
		address = []byte(row.Address)
	}
	verbAction := releaseActionString(success)
	outcome := OutcomeCommit
	if !success {
		outcome = OutcomeAbandon
	}
	hint := ClaimLineageHint{
		InstanceID: acq.InstanceID,
		FrameID:    acq.FrameID,
		NodeRunID:  acq.NodeRunID,
		NodeID:     acq.NodeID,
	}
	if row != nil && row.ProducerName != nil {
		hint.ProducerName = *row.ProducerName
	}
	if row != nil {
		hint.VersionID = row.VersionID
	}
	var lifetime spec.ClaimLifetime
	var candidateHandle []byte
	producerName := ""
	var parentClaimHandleID *shared.UUID
	if row != nil {
		lifetime = row.Lifetime
		candidateHandle = row.ProducerCandidateHandle
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		parentClaimHandleID = row.ParentClaimHandleID
	}
	pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
		ClaimHandleID:       lk.ClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              ActiveTerminal,
		Outcome:             outcome,
		Producer:            lk.Producer,
		Scope:               scope,
		Address:             address,
		LeaseToken:          lk.ProducerLeaseToken,
		Lifetime:            lifetime,
		CandidateHandle:     candidateHandle,
		ProducerName:        producerName,
		LineageHint:         hint,
		ParentClaimHandleID: parentClaimHandleID,
	}, tx)
	if err != nil {
		return nil, fmt.Errorf("releaseClaim: %w", err)
	}
	if err := emitLockReleased(ctx, args, acq, lk, verbAction, tx); err != nil {
		return nil, err
	}
	return pc, nil
}

func releaseInheritedClaimsInTx(
	ctx context.Context, args RunArgs, acq *acquisition, success bool, tx persistence.Tx,
) (postCommitFn, error) {
	inherited, err := findInheritedAliasesForRun(ctx, args, acq.HeldSubgraphs, acq.NodeType, acq.NodeRunID, tx)
	if err != nil {
		return nil, err
	}
	var post postCommitFn
	for _, ia := range inherited {
		if err := markClaimHolderForRun(ctx, args, ia.ClaimHandleID, acq.NodeRunID, success, tx); err != nil {
			return nil, err
		}
		pc, err := CheckAndFireResolution(ctx, args, ia.ClaimHandleID, tx)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}

func releaseActionString(success bool) string {
	if success {
		return "release"
	}
	return "release_failed"
}

func emitLockReleased(
	ctx context.Context, args RunArgs, acq *acquisition, lk AcquiredLock, action string, tx persistence.Tx,
) error {
	payload := &genv1.LockReleasedPayload{
		HolderId:     lk.ClaimHandleID.String(),
		SupervisorId: args.SupervisorID,
		Action:       action,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload.LockKind = string(persistence.LockKindNamed)
		payload.LockName = sp.Name
	case claimproducer.ClaimSpec:
		payload.LockKind = string(persistence.LockKindScope)
		payload.ProducerName = sp.ProducerName
		payload.Alias = sp.Alias
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindLockReleased(), Payload: eventpayload.New(payload),
	}, tx); err != nil {
		return fmt.Errorf("emitLockReleased: %w", err)
	}
	return nil
}
