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
	payload := map[string]any{
		"source":        "acquire_unavailable",
		"unavailable":   producerNameForSpec(acq.UnavailableSpec),
		"partial_locks": len(acq.PartialLocks),
		"dispatch_id":   cand.NodeRunID.String(),
		"node_id":       cand.NodeID.String(),
		"node_type":     acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, primaryClass, acquireUnavailableSyntheticClass, payload, "handleAcquireUnavailable")
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
	payload := map[string]any{
		"source":        "acquire_producer_error",
		"producer":      producerNameForSpec(acq.ErroredSpec),
		"partial_locks": len(acq.PartialLocks),
		"dispatch_id":   cand.NodeRunID.String(),
		"node_id":       cand.NodeID.String(),
		"node_type":     acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, primaryClass, producerAcquireErrorFallbackClass, payload, "handleAcquireProducerError")
}

func runAcquireErrorPolicy(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass, fallbackClass string, payload map[string]any, site string,
) *policyDecision {
	var post postCommitFn
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := applyErrorPolicyWithScratch(ctx, args, acq, errorClass, fallbackClass, payload, nil, nil, nil, tx)
		post = p
		return err
	}); err != nil {
		args.Logger.Warn(site+": applyErrorPolicy failed",
			"node_id", acq.NodeID.String(),
			"dispatch_id", acq.NodeRunID.String(),
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
		c, err := args.Queue.ClaimDispatchRow(ctx, tx, cand.NodeRunID, args.SupervisorID)
		claimed = c
		return err
	}); err != nil {
		args.Logger.Warn(site+": re-claim dispatch row failed; skipping policy resolution this cycle",
			"node_id", cand.NodeID.String(),
			"dispatch_id", cand.NodeRunID.String(),
			"error", err.Error())
		return false
	}
	if !claimed {
		args.Logger.Info(site+": dispatch row no longer claimable; another resolution owns it",
			"node_id", cand.NodeID.String(),
			"dispatch_id", cand.NodeRunID.String())
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
		abandonBegunCandidate(ctx, args, lk)
		scope := claimScope(lk)
		address := claimAddress(lk)
		if err := abandonOpenedClaim(ctx, lk.Producer, lk.ClaimHandleID, scope, address); err != nil {
			args.Logger.Warn("abandonPartialLocks: Abandon failed",
				"producer", producerNameForSpec(lk.Spec), "error", err.Error())
		}
	}
}

// @concept: data-processing
func abandonBegunCandidate(ctx context.Context, args RunArgs, lk AcquiredLock) {
	if len(lk.ProducerCandidateHandle) == 0 || args.DataProcessors == nil {
		return
	}
	producerName := producerNameForSpec(lk.Spec)
	dp, ok := args.DataProcessors.Get(producerName)
	if !ok {
		return
	}
	if err := dp.AbandonCandidate(ctx, AbandonCandidateInput{
		ProducerName:    producerName,
		ClaimHandleID:   lk.ClaimHandleID.String(),
		CandidateHandle: lk.ProducerCandidateHandle,
	}); err != nil {
		args.Logger.Warn("abandonPartialLocks: AbandonCandidate failed",
			"producer", producerName, "error", err.Error())
	}
}
