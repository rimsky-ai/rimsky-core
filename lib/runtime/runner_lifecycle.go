// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: terminal-resolution.
//
//	@concept: error-policy
func handleAcquireUnavailable(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
	if acq.NodeDef == nil {
		return
	}
	if args.PreAcquireUnavailableHook != nil {
		args.PreAcquireUnavailableHook(ctx)
	}
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireUnavailable") {
		return
	}

	errorClass := acquireUnavailableSyntheticClass
	if acq.UnavailableClass != "" {
		errorClass = acq.UnavailableClass
	}
	if err := OnError(ctx, OnErrorArgs{
		Persist:             args.Persist,
		Queue:               args.Queue,
		Clock:               args.Clock,
		Logger:              args.Logger,
		NodeID:              cand.NodeID,
		RunScopeID:          acq.RunScopeID,
		InstanceID:          acq.InstanceID,
		SupervisorID:        args.SupervisorID,
		ErrorClass:          errorClass,
		PolicyFallbackClass: acquireUnavailableSyntheticClass,
		Payload: map[string]any{
			"source":        "acquire_unavailable",
			"unavailable":   producerNameForSpec(acq.UnavailableSpec),
			"partial_locks": len(acq.PartialLocks),
			"dispatch_id":   cand.DispatchID.String(),
			"node_id":       cand.NodeID.String(),
			"node_type":     acq.NodeType,
		},
		Metrics: args.Metrics,
	}); err != nil {
		args.Logger.Warn("handleAcquireUnavailable: OnError failed",
			"node_id", cand.NodeID.String(),
			"dispatch_id", cand.DispatchID.String(),
			"error", err.Error())
	}
}

const acquireUnavailableSyntheticClass = "acquire/unavailable"

const producerAcquireErrorFallbackClass = "acquire/producer_error"

// @concept: error-policy
func handleAcquireProducerError(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
	if acq.NodeDef == nil {
		return
	}
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireProducerError") {
		return
	}

	errorClass := acq.ProducerErrorClass
	if errorClass == "" {
		errorClass = producerAcquireErrorFallbackClass
	}
	if err := OnError(ctx, OnErrorArgs{
		Persist:             args.Persist,
		Queue:               args.Queue,
		Clock:               args.Clock,
		Logger:              args.Logger,
		NodeID:              cand.NodeID,
		RunScopeID:          acq.RunScopeID,
		InstanceID:          acq.InstanceID,
		SupervisorID:        args.SupervisorID,
		ErrorClass:          errorClass,
		PolicyFallbackClass: producerAcquireErrorFallbackClass,
		Payload: map[string]any{
			"source":        "acquire_producer_error",
			"producer":      producerNameForSpec(acq.ErroredSpec),
			"partial_locks": len(acq.PartialLocks),
			"dispatch_id":   cand.DispatchID.String(),
			"node_id":       cand.NodeID.String(),
			"node_type":     acq.NodeType,
		},
		Metrics: args.Metrics,
	}); err != nil {
		args.Logger.Warn("handleAcquireProducerError: OnError failed",
			"node_id", cand.NodeID.String(),
			"dispatch_id", cand.DispatchID.String(),
			"error", err.Error())
	}
}

func reclaimDispatchRowShortTx(ctx context.Context, args RunArgs, cand persistence.Candidate, site string) bool {
	var claimed bool
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		c, err := args.Queue.ClaimDispatchRow(ctx, tx, cand.DispatchID, args.SupervisorID)
		claimed = c
		return err
	}); err != nil {
		args.Logger.Warn(site+": re-claim dispatch row failed; skipping policy resolution this cycle",
			"node_id", cand.NodeID.String(),
			"dispatch_id", cand.DispatchID.String(),
			"error", err.Error())
		return false
	}
	if !claimed {
		args.Logger.Info(site+": dispatch row no longer claimable; another resolution owns it",
			"node_id", cand.NodeID.String(),
			"dispatch_id", cand.DispatchID.String())
		return false
	}
	return true
}

// @concept: terminal-resolution
func abandonPartialLocks(ctx context.Context, args RunArgs, partial []AcquiredLock) {
	for _, lk := range partial {
		if lk.Producer == nil {
			continue
		}
		scope := claimScope(lk)
		address := claimAddress(lk)
		if err := abandonOpenedClaim(ctx, lk.Producer, lk.ClaimHandleID, scope, address); err != nil {
			args.Logger.Warn("handleAcquireUnavailable: Abandon failed",
				"producer", producerNameForSpec(lk.Spec), "error", err.Error())
		}
	}
}
