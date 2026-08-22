// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: instance
// @concept: terminal-resolution

package runtime

import (
	"context"
	"errors"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func ForceFailInFlightRunsForInstance(
	ctx context.Context, args RunArgs, instanceID shared.UUID, tx persistence.Tx,
) (func(context.Context), error) {
	runs, err := args.Persist.Nodes().ListRunsForInstanceByStates(ctx, instanceID, cascade.InFlightStates, tx)
	if err != nil {
		return nil, err
	}
	var post postCommitFn
	for _, r := range runs {
		pc, err := forceFailRunInstanceKilled(ctx, args, instanceID, r, tx)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, pc)
	}
	if post == nil {
		return func(context.Context) {}, nil
	}
	return post, nil
}

func forceFailRunInstanceKilled(
	ctx context.Context, args RunArgs, instanceID shared.UUID, run persistence.NodeRunLatest, tx persistence.Tx,
) (postCommitFn, error) {
	current, err := args.Persist.Nodes().GetRunForGate(ctx, run.NodeRunID, tx)
	if err != nil {
		return nil, err
	}
	if current == nil || cascade.IsTerminal(current.State) {
		return nil, nil
	}
	sig := cascade.SettlingSignalInstanceKilled
	if err := args.Persist.Nodes().UpdateState(ctx, run.NodeRunID,
		cascade.NodeStateFailed, cascade.ReasonInstanceKilled, &sig, tx); err != nil {
		if errors.Is(err, cascade.ErrIllegalTransition) {
			if args.Logger != nil {
				args.Logger.Warn("forceFailRunInstanceKilled: raced with the run's own terminal transition; leaving its outcome as-is",
					"node_run_id", run.NodeRunID.String(), "error", err.Error())
			}
			return nil, nil
		}
		return nil, err
	}
	if err := AppendStateTransitionEvent(ctx, args.Persist, run.NodeID, instanceID,
		current.State, cascade.NodeStateFailed, cascade.ReasonInstanceKilled, tx); err != nil {
		return nil, err
	}
	var nodeType string
	nodeRow, err := args.Persist.Nodes().Get(ctx, run.NodeID, tx)
	if err != nil {
		return nil, err
	}
	if nodeRow != nil {
		nodeType = nodeRow.NodeType
	}
	// @concept: signal
	killSig := signalpkg.BuildTerminalErrorSignal(cascade.ErrorClassInstanceKilled, nil, 0, 0, nil, nil)
	if err := emitSignalInTxOnce(ctx, args, run.NodeID, nodeType, run.NodeRunID,
		instanceID, run.FrameID, killSig, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("forceFailRunInstanceKilled: terminal signal emission failed; kill stands",
			"node_run_id", run.NodeRunID.String(), "error", err.Error())
	}
	if err := drainWaitSetOnSettled(ctx, args, run.FrameID, run.NodeRunID, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("forceFailRunInstanceKilled: wait-set drain failed; kill stands",
			"node_run_id", run.NodeRunID.String(), "error", err.Error())
	}
	if current.State == cascade.NodeStateHeld {
		failHeldCoHolderRows(ctx, args, run.NodeRunID, tx)
	}
	post := abandonRunClaimsThroughProducers(ctx, args, instanceID, run, tx)
	runID := run.NodeRunID
	propagate := func(pctx context.Context) {
		if _, err := PropagateIfChildAfterTerminal(pctx, args, runID); err != nil && args.Logger != nil {
			args.Logger.Warn("forceFailRunInstanceKilled: run-tree propagation failed",
				"run_id", runID.String(), "error", err.Error())
		}
	}
	return chainPostCommit(post, propagate), nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func failHeldCoHolderRows(
	ctx context.Context, args RunArgs, holderNodeRunID shared.UUID, tx persistence.Tx,
) {
	if args.Persist.ClaimHolders() == nil {
		return
	}
	holders, err := args.Persist.ClaimHolders().ListByHolderRun(ctx, holderNodeRunID, tx)
	if err != nil {
		if args.Logger != nil {
			args.Logger.Warn("failHeldCoHolderRows: list claim holders failed",
				"holder_run_id", holderNodeRunID.String(), "error", err.Error())
		}
		return
	}
	for _, h := range holders {
		handle, err := args.ClaimHandles.Get(ctx, h.ClaimHandleID, tx)
		if err != nil {
			if args.Logger != nil {
				args.Logger.Warn("failHeldCoHolderRows: load claim handle failed",
					"claim_handle_id", h.ClaimHandleID.String(), "error", err.Error())
			}
			continue
		}
		if handle == nil || handle.HolderSupervisorID == nil {
			continue
		}
		if err := args.Persist.ClaimHolders().FailAllActiveByClaimHandle(ctx, h.ClaimHandleID, *handle.HolderSupervisorID, tx); err != nil && args.Logger != nil {
			args.Logger.Warn("failHeldCoHolderRows: fail active holders failed",
				"claim_handle_id", h.ClaimHandleID.String(), "error", err.Error())
		}
	}
}

// @concept: claim-handle
// @concept: terminal-resolution
func abandonRunClaimsThroughProducers(
	ctx context.Context, args RunArgs, instanceID shared.UUID, run persistence.NodeRunLatest, tx persistence.Tx,
) postCommitFn {
	if args.ClaimHandles == nil {
		return nil
	}
	handles, err := args.ClaimHandles.ListByNodeRun(ctx, run.NodeRunID, tx)
	if err != nil {
		if args.Logger != nil {
			args.Logger.Warn("abandonRunClaimsThroughProducers: list claim handles failed",
				"node_run_id", run.NodeRunID.String(), "error", err.Error())
		}
		return nil
	}
	var post postCommitFn
	for i := range handles {
		h := handles[i]
		if h.State != spec.ClaimHandleStateActive || h.IsHeld || h.HolderSupervisorID == nil {
			continue
		}
		if h.LockKind == persistence.LockKindNamed {
			if err := args.ClaimHandles.Delete(ctx, h.ID, *h.HolderSupervisorID, tx); err != nil && args.Logger != nil {
				args.Logger.Warn("abandonRunClaimsThroughProducers: release named lock failed",
					"claim_handle_id", h.ID.String(), "error", err.Error())
			}
			continue
		}
		pc := resolveKilledClaimTerminal(ctx, args, instanceID, run, h, tx)
		post = chainPostCommit(post, pc)
	}
	return post
}

func resolveKilledClaimTerminal(
	ctx context.Context, args RunArgs, instanceID shared.UUID, run persistence.NodeRunLatest, h persistence.ClaimHandleRow, tx persistence.Tx,
) postCommitFn {
	producerName := ""
	if h.ProducerName != nil {
		producerName = *h.ProducerName
	}
	var producer locks.ClaimProducer
	producerOK := false
	if args.ClaimProducerRegistry != nil {
		p, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, producerName, "", tx)
		if resolveErr != nil {
			if args.Logger != nil {
				args.Logger.Warn("resolveKilledClaimTerminal: producer resolution failed transiently; record-only abandon",
					"claim_handle_id", h.ID.String(), "producer", producerName, "error", resolveErr.Error())
			}
		} else {
			producer, producerOK = p, ok
		}
	}
	if producerOK {
		pc, err := ResolveClaimHandleTerminal(ctx, args, TerminalDecision{
			ClaimHandleID:   h.ID,
			SupervisorID:    *h.HolderSupervisorID,
			Source:          ActiveTerminal,
			Outcome:         OutcomeAbandon,
			Producer:        producer,
			Scope:           []byte(h.ClaimScopeData),
			Address:         []byte(h.Address),
			LeaseToken:      h.ProducerLeaseToken,
			Lifetime:        h.Lifetime,
			CandidateHandle: h.ProducerCandidateHandle,
			ProducerName:    producerName,
			LineageHint: ClaimLineageHint{
				InstanceID:   instanceID,
				FrameID:      run.FrameID,
				NodeRunID:    run.NodeRunID,
				NodeID:       run.NodeID,
				ProducerName: producerName,
				VersionID:    h.VersionID,
			},
			ParentClaimHandleID: h.ParentClaimHandleID,
		}, tx)
		if err == nil {
			return pc
		}
		if args.Logger != nil {
			args.Logger.Warn("resolveKilledClaimTerminal: producer-notified abandon failed; falling back to record-only abandon",
				"claim_handle_id", h.ID.String(), "producer", producerName, "error", err.Error())
		}
	} else if args.Logger != nil {
		args.Logger.Warn("resolveKilledClaimTerminal: producer unavailable; record-only abandon",
			"claim_handle_id", h.ID.String(), "producer", producerName)
	}
	if err := args.ClaimHandles.Promote(ctx, h.ID, *h.HolderSupervisorID,
		spec.ClaimHandleStateAbandoned, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("resolveKilledClaimTerminal: record-only abandon failed",
			"claim_handle_id", h.ID.String(), "error", err.Error())
	}
	return nil
}
