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
) error {
	for _, lk := range acq.Locks {
		if err := releaseAcquiredLock(ctx, args, tx, acq, lk, success); err != nil {
			return err
		}
	}
	if !retainLinkedSubClaims {
		if err := resolveLinkedSubClaimsInTx(ctx, args, tx, acq, success); err != nil {
			return err
		}
	}
	return releaseInheritedClaimsInTx(ctx, args, tx, acq, success)
}

// @concept: claim-tree
// @concept: fan-out
func resolveLinkedSubClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	if args.ClaimHandles == nil {
		return nil
	}
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, acq.DispatchID, tx)
	if err != nil {
		return fmt.Errorf("resolveLinkedSubClaims: ListByNodeRun: %w", err)
	}
	released := make(map[shared.UUID]bool, len(acq.Locks))
	for _, lk := range acq.Locks {
		released[lk.ClaimHandleID] = true
	}
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
			return fmt.Errorf("resolveLinkedSubClaims: unknown producer %q for sub-claim %s", producerName, row.ID)
		}
		outcome := OutcomeCommit
		if !success {
			outcome = OutcomeAbandon
		}
		hint := ClaimLineageHint{
			InstanceID:   acq.InstanceID,
			FrameID:      acq.FrameID,
			RunID:        acq.DispatchID,
			NodeID:       acq.NodeID,
			ProducerName: producerName,
			VersionID:    row.VersionID,
		}
		if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
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
		}); err != nil {
			return fmt.Errorf("resolveLinkedSubClaims: sub-claim %s: %w", row.ID, err)
		}
	}
	return nil
}

func releaseAcquiredLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, success bool,
) error {
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		_ = sp
		if err := args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
			return fmt.Errorf("releaseAcquiredLock: named Delete: %w", err)
		}
		return emitLockReleased(ctx, args, tx, acq, lk, releaseActionString(success))
	case claimproducer.ClaimSpec:
		return releaseClaim(ctx, args, tx, acq, lk, sp, success)
	}
	return fmt.Errorf("releaseAcquiredLock: unknown spec %T", lk.Spec)
}

func releaseClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	acq *acquisition, lk AcquiredLock, claimSpec claimproducer.ClaimSpec, success bool,
) error {
	held := isAliasHeld(acq.HeldSubgraphs, acq.NodeType, claimSpec.Alias)
	if held {
		if err := markClaimHolderForRun(ctx, args, tx, lk.ClaimHandleID, acq.DispatchID, success); err != nil {
			return err
		}
		if !success {
			if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, lk.ClaimHandleID, args.SupervisorID, tx); err != nil {
				return fmt.Errorf("releaseClaim: fail inheritors: %w", err)
			}
		}
		if err := CheckAndFireResolution(ctx, args, tx, lk.ClaimHandleID); err != nil {
			return err
		}
		return emitLockReleased(ctx, args, tx, acq, lk, "held_marked")
	}
	row, err := args.ClaimHandles.Get(ctx, lk.ClaimHandleID, tx)
	if err != nil {
		return fmt.Errorf("releaseClaim: load scope/address: %w", err)
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
		RunID:      acq.DispatchID,
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
	if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
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
	}); err != nil {
		return fmt.Errorf("releaseClaim: %w", err)
	}
	return emitLockReleased(ctx, args, tx, acq, lk, verbAction)
}

func releaseInheritedClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, success bool,
) error {
	inherited, err := findInheritedAliasesForRun(ctx, args, tx, acq.HeldSubgraphs, acq.NodeType, acq.DispatchID, acq.InstanceID)
	if err != nil {
		return err
	}
	for _, ia := range inherited {
		if err := markClaimHolderForRun(ctx, args, tx, ia.ClaimHandleID, acq.DispatchID, success); err != nil {
			return err
		}
		if err := CheckAndFireResolution(ctx, args, tx, ia.ClaimHandleID); err != nil {
			return err
		}
	}
	return nil
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
