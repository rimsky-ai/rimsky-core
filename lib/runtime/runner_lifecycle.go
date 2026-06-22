// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const acquireUnavailableSyntheticClass = "acquire/unavailable"

const producerAcquireErrorFallbackClass = "acquire/producer_error"

// @concept: terminal-resolution
// @concept: error-policy
// @decision: in-place-retry
func handleAcquireUnavailable(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) *policyDecision {
	if acq.NodeDef == nil {
		return nil
	}
	if args.PreAcquireUnavailableHook != nil {
		args.PreAcquireUnavailableHook(ctx)
	}
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireUnavailable") {
		return nil
	}

	primaryClass := acquireUnavailableSyntheticClass
	if acq.UnavailableClass != "" {
		primaryClass = acq.UnavailableClass
	}
	if _, declared := acq.NodeDef.ErrorTypes[primaryClass]; !declared {
		if _, fallback := acq.NodeDef.ErrorTypes[acquireUnavailableSyntheticClass]; !fallback {
			if err := args.Queue.ReleaseClaim(ctx, cand.DispatchID, args.SupervisorID); err != nil {
				args.Logger.Warn("handleAcquireUnavailable: ReleaseClaim failed; row may stay claimed until liveness sweep",
					"node_id", cand.NodeID.String(),
					"dispatch_id", cand.DispatchID.String(),
					"error", err.Error())
			}
			return nil
		}
	}
	effectiveClass := resolveErrorPolicyClass(acq.NodeDef, primaryClass, acquireUnavailableSyntheticClass)
	payload := map[string]any{
		"source":        "acquire_unavailable",
		"unavailable":   producerNameForSpec(acq.UnavailableSpec),
		"partial_locks": len(acq.PartialLocks),
		"dispatch_id":   cand.DispatchID.String(),
		"node_id":       cand.NodeID.String(),
		"node_type":     acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, effectiveClass, payload, "handleAcquireUnavailable")
}

// @concept: error-policy
// @decision: in-place-retry
func handleAcquireProducerError(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) *policyDecision {
	if acq.NodeDef == nil {
		return nil
	}
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireProducerError") {
		return nil
	}

	primaryClass := acq.ProducerErrorClass
	if primaryClass == "" {
		primaryClass = producerAcquireErrorFallbackClass
	}
	effectiveClass := resolveErrorPolicyClass(acq.NodeDef, primaryClass, producerAcquireErrorFallbackClass)
	payload := map[string]any{
		"source":        "acquire_producer_error",
		"producer":      producerNameForSpec(acq.ErroredSpec),
		"partial_locks": len(acq.PartialLocks),
		"dispatch_id":   cand.DispatchID.String(),
		"node_id":       cand.NodeID.String(),
		"node_type":     acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, effectiveClass, payload, "handleAcquireProducerError")
}

func runAcquireErrorPolicy(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, site string,
) *policyDecision {
	var post postCommitFn
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := applyErrorPolicy(ctx, args, acq, errorClass, payload, tx)
		post = p
		return err
	}); err != nil {
		args.Logger.Warn(site+": applyErrorPolicy failed",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.DispatchID.String(),
			"error", err.Error())
		return nil
	}
	if post != nil {
		post(ctx)
	}
	if acq.RetryDecision != nil && acq.RetryDecision.IsRetry() {
		return acq.RetryDecision
	}
	return nil
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
