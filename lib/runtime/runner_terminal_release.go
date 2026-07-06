// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
)

func releaseLocksInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition,
	success bool, retainLinkedSubClaims bool,
) (postCommitFn, error) {
	var post postCommitFn
	for _, lk := range acq.Locks {
		pc, err := releaseAcquiredLock(ctx, args, tx, acq, lk, success)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, pc)
	}
	if !retainLinkedSubClaims {
		pc, err := resolveLinkedSubClaimsInTx(ctx, args, tx, acq, success)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, pc)
	}
	pc, err := releaseInheritedClaimsInTx(ctx, args, tx, acq, success)
	if err != nil {
		return nil, err
	}
	post = chainPostCommit(post, pc)
	return post, nil
}

// @concept: claim-tree
// @concept: fan-out
func resolveLinkedSubClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
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
		if row.IsHeld || row.State != spec.ClaimHandleStateActive {
			continue
		}
		producerName := ""
		if row.ProducerName != nil {
			producerName = *row.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
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
		pc, err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       row.ID,
			SupervisorID:        args.SupervisorID,
			Source:              ActiveTerminal,
			Outcome:             outcome,
			Producer:            producer,
			Scope:               []byte(row.ClaimScopeData),
			Address:             []byte(row.Address),
			Lifetime:            row.Lifetime,
			CandidateHandle:     row.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: row.ParentClaimHandleID,
		})
		if err != nil {
			return nil, fmt.Errorf("resolveLinkedSubClaims: sub-claim %s: %w", row.ID, err)
		}
		post = chainPostCommit(post, pc)
	}
	return post, nil
}

func releaseAcquiredLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, success bool,
) (postCommitFn, error) {
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		_ = sp
		if err := args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
			return nil, fmt.Errorf("releaseAcquiredLock: named Delete: %w", err)
		}
		return nil, emitLockReleased(ctx, args, tx, acq, lk, releaseActionString(success))
	case claimproducer.ClaimSpec:
		return releaseClaim(ctx, args, tx, acq, lk, sp, success)
	}
	return nil, fmt.Errorf("releaseAcquiredLock: unknown spec %T", lk.Spec)
}

func releaseClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, claimSpec claimproducer.ClaimSpec, success bool,
) (postCommitFn, error) {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, claimSpec.Alias)
	if held {
		if err := markClaimHolderForRun(ctx, args, tx, lk.ClaimHandleID, acq.NodeRunID, success); err != nil {
			return nil, err
		}
		if !success {
			if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
				return nil, fmt.Errorf("releaseClaim: fail inheritors: %w", err)
			}
		}
		pc, err := CheckAndFireResolution(ctx, args, tx, lk.ClaimHandleID)
		if err != nil {
			return nil, err
		}
		if err := emitLockReleased(ctx, args, tx, acq, lk, "held_marked"); err != nil {
			return nil, err
		}
		return pc, nil
	}
	row, err := args.ClaimHandles.Get(ctx, lk.ClaimHandleID, tx)
	if err != nil {
		return nil, fmt.Errorf("releaseClaim: load scope/address: %w", err)
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
	pc, err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
		ClaimHandleID:       lk.ClaimHandleID,
		SupervisorID:        args.SupervisorID,
		Source:              ActiveTerminal,
		Outcome:             outcome,
		Producer:            lk.Producer,
		Scope:               scope,
		Address:             address,
		Lifetime:            lifetime,
		CandidateHandle:     candidateHandle,
		ProducerName:        producerName,
		LineageHint:         hint,
		ParentClaimHandleID: parentClaimHandleID,
	})
	if err != nil {
		return nil, fmt.Errorf("releaseClaim: %w", err)
	}
	if err := emitLockReleased(ctx, args, tx, acq, lk, verbAction); err != nil {
		return nil, err
	}
	return pc, nil
}

func releaseInheritedClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) (postCommitFn, error) {
	inherited, err := findInheritedAliasesForRun(ctx, args, tx, acq.HeldSubgraphs, acq.NodeType, acq.NodeRunID, acq.InstanceID)
	if err != nil {
		return nil, err
	}
	var post postCommitFn
	for _, ia := range inherited {
		if err := markClaimHolderForRun(ctx, args, tx, ia.ClaimHandleID, acq.NodeRunID, success); err != nil {
			return nil, err
		}
		pc, err := CheckAndFireResolution(ctx, args, tx, ia.ClaimHandleID)
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
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, action string,
) error {
	payload := map[string]any{
		"holder_id":     lk.ClaimHandleID.String(),
		"supervisor_id": args.SupervisorID,
		"action":        action,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case claimproducer.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["producer_name"] = sp.ProducerName
		payload["alias"] = sp.Alias
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: events.KindLockReleased(), Payload: payload,
	}, tx); err != nil {
		return fmt.Errorf("emitLockReleased: %w", err)
	}
	return nil
}
