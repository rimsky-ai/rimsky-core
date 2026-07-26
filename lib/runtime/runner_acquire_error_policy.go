// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const acquireUnavailableSyntheticClass = "acquire/unavailable"

const producerAcquireErrorFallbackClass = "acquire/producer_error"

// @concept: service-address-book
const unresolvedClaimProducerSyntheticClass = "acquire/unresolved_claim_producer"

const nilFrameIDSyntheticClass = "acquire/nil_frame_id"

const fanOutSubstitutionFailedSyntheticClass = "acquire/fan_out_partition_request_substitution_failed"

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

// @concept: frame
// @concept: error-policy
func handleAcquireNilFrameID(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) *policyDecision {
	if acq.NodeDef == nil {
		return nil
	}
	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireNilFrameID") {
		return nil
	}
	payload := map[string]any{
		"source":      "acquire_nil_frame_id",
		"dispatch_id": cand.NodeRunID.String(),
		"node_id":     cand.NodeID.String(),
		"node_type":   acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, nilFrameIDSyntheticClass, nilFrameIDSyntheticClass, payload, "handleAcquireNilFrameID")
}

// @concept: fan-out
// @concept: error-policy
func handleAcquireFanOutSubstitutionFailed(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) *policyDecision {
	if acq.NodeDef == nil {
		return nil
	}
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireFanOutSubstitutionFailed") {
		return nil
	}

	payload := map[string]any{
		"source":        "acquire_fan_out_partition_request_substitution_failed",
		"error":         acq.FanOutSubstitutionErr,
		"partial_locks": len(acq.PartialLocks),
		"dispatch_id":   cand.NodeRunID.String(),
		"node_id":       cand.NodeID.String(),
		"node_type":     acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, fanOutSubstitutionFailedSyntheticClass, fanOutSubstitutionFailedSyntheticClass, payload, "handleAcquireFanOutSubstitutionFailed")
}

// @concept: error-policy
// @decision: substitution-failure-routes-with-substitution
func handleAcquireLockSpecSubstitutionFailed(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) *policyDecision {
	if acq.NodeDef == nil {
		return nil
	}
	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireLockSpecSubstitutionFailed") {
		return nil
	}
	emitAttributeFailureEvent(ctx, args, acq.NodeID, acq.InstanceID,
		events.KindTemplateResolutionFailed(), acq.LockSpecSubstitutionDirective,
		acq.LockSpecSubstitutionSite, "", acq.LockSpecSubstitutionErr)
	payload := map[string]any{
		"source":      "acquire_lock_spec_substitution_failed",
		"site":        acq.LockSpecSubstitutionSite,
		"directive":   acq.LockSpecSubstitutionDirective,
		"error":       acq.LockSpecSubstitutionErr,
		"dispatch_id": cand.NodeRunID.String(),
		"node_id":     cand.NodeID.String(),
		"node_type":   acq.NodeType,
	}
	return runAcquireErrorPolicy(ctx, args, &acq, spec.ErrorClassTemplateResolutionFailed, spec.ErrorClassTemplateResolutionFailed, payload, "handleAcquireLockSpecSubstitutionFailed")
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
		c, err := args.Queue.ClaimDispatchRow(ctx, cand.NodeRunID, args.SupervisorID, tx)
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
	outbox := ProducerVerbOutboxOf(args)
	for _, lk := range partial {
		if lk.Producer == nil {
			continue
		}
		abandonBegunCandidate(ctx, args, lk)
		scope := claimScope(lk)
		address := claimAddress(lk)
		if outbox != nil && args.Clock != nil {
			now := args.Clock.Now()
			if err := outbox.Enqueue(ctx, persistence.ProducerVerbOutboxInsertInput{
				ClaimHandleID:  lk.ClaimHandleID,
				ProducerName:   producerNameForSpec(lk.Spec),
				Verb:           persistence.ProducerVerbAbandon,
				ClaimScopeData: scope,
				Address:        address,
				LeaseToken:     lk.ProducerLeaseToken,
				SupervisorID:   args.SupervisorID,
				NextAttemptAt:  now,
				EnqueuedAt:     now,
			}, nil); err != nil {
				args.Logger.Warn("abandonPartialLocks: enqueue Abandon failed",
					"producer", producerNameForSpec(lk.Spec), "error", err.Error())
			}
			continue
		}
		if err := abandonOpenedClaim(ctx, lk.Producer, lk.ClaimHandleID, scope, address, lk.ProducerLeaseToken); err != nil {
			args.Logger.Warn("abandonPartialLocks: Abandon failed",
				"producer", producerNameForSpec(lk.Spec), "error", err.Error())
		}
	}
	if len(partial) > 0 && args.ProducerVerbKick != nil {
		args.ProducerVerbKick()
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
