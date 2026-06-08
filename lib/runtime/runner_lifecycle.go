// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pre-dispatch acquisition-failure routing. The three declarative
// lifecycle-handler slots (on_acquire_unavailable, on_executor_complete,
// on_executor_errored) retired 2026-05-23 per
// `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-
// design.md`:
//
//   - on_acquire_unavailable → folded into the operator's `error_types:`
//     chain via synthetic class "acquire/unavailable" (this file).
//   - on_executor_complete   → subscriber-side cascade-fire is now
//     purely subscription-driven; `payload.changed` is a CEL-readable
//     field of `terminal/success`. No replacement slot needed.
//   - on_executor_errored    → folded into `error_types:` directly; a
//     per-class `pass` action replaces the handler's `resolve: pass`.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// handleAcquireUnavailable routes pre-dispatch acquisition failure
// through the operator's error_types: chain via synthetic class
// "acquire/unavailable". No implicit retry; an operator that wants
// retry declares it explicitly in error_types:. See the validator
// warning at `graph/node/template_validator.go::
// validateAcquireUnavailablePolicyAdvised` that surfaces the absence
// at template-deploy time.
//
// Tx handling: the caller's per-candidate acquisition tx already
// rolled back via the errAcquireUnavailable sentinel (see
// runner_acquire.go:280). This function opens fresh transactions
// internally — the OnError dispatch path does its own tx work, and the
// partial-lock Abandon cleanup runs outside any rimsky-side tx.
//
//	@concept: error-policy
func handleAcquireUnavailable(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
	// Defense-in-depth nil check: today's tryAcquire path that returns
	// errAcquireUnavailable always populates NodeDef (an Unavailable
	// requires having a ClaimSpec, which requires a template lookup).
	if acq.NodeDef == nil {
		return
	}
	// Abandon any claims that successfully Open'd before the Unavailable
	// was hit. The tx-side rollback already removed the lock-holder
	// rows; the store-side Abandon undoes any producer-side state.
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	// Route through the operator's error_types: chain. Key on the
	// producer-declared acquisition-failure class (e.g.
	// "pg/claim_unavailable") when the producer named one on its
	// Unavailable response; otherwise fall back to the synthetic
	// "acquire/unavailable". Threading the producer-declared leaf is what
	// makes an operator's `error_types: { pg/claim_unavailable: ... }`
	// policy match — and what lands the canonical
	// `terminal/error/pg/claim_unavailable` signal on the event log a
	// subscriber routes to. Absent policy → OnError resolves to
	// give_up("unknown_error_class") (intentional fail-fast; operators
	// that want retry declare it explicitly).
	errorClass := acquireUnavailableSyntheticClass
	if acq.UnavailableClass != "" {
		errorClass = acq.UnavailableClass
	}
	if err := OnError(ctx, OnErrorArgs{
		Persist:      args.Persist,
		Queue:        args.Queue,
		Clock:        args.Clock,
		Logger:       args.Logger,
		NodeID:       cand.NodeID,
		RunScopeID:   acq.RunScopeID,
		InstanceID:   acq.InstanceID,
		SupervisorID: args.SupervisorID,
		ErrorClass:   errorClass,
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

// acquireUnavailableSyntheticClass is the default synthetic error_class
// rimsky keys the operator's `error_types:` chain on when a producer
// returned Unavailable WITHOUT naming a producer-declared class. A
// producer that names a class on its Unavailable response (e.g. the
// postgres store's "pg/claim_unavailable") overrides this so the
// operator's chain matches the declared leaf.
const acquireUnavailableSyntheticClass = "acquire/unavailable"

// producerAcquireErrorFallbackClass is the synthetic error_class used
// when a faulted claim-producer Open RPC attached no
// google.rpc.ErrorInfo detail (so the translator recovered ""). Absent
// an operator-declared policy for this class, OnError resolves to
// give_up("unknown_error_class") — the same fail-fast default as
// "acquire/unavailable". A host-agent-proxy fronting the claim-producer
// protocol stamps concrete classes (spawn_failed, host_agent_disconnected,
// binding_not_found, ...) via ErrorInfo.Reason, so this fallback only
// fires for direct-dial producers that fault with a bare gRPC status.
const producerAcquireErrorFallbackClass = "acquire/producer_error"

// handleAcquireProducerError routes a pre-dispatch claim-producer Open
// fault through the operator's error_types: chain via the producer's
// translated error_class (the gRPC ErrorInfo.Reason carried on
// acq.ProducerErrorClass), falling back to the synthetic
// producerAcquireErrorFallbackClass when the producer attached no
// ErrorInfo. This is the claim-producer analogue of
// handleAcquireUnavailable — the same chain executor Error{error_class}
// terminals use, with no new policy mechanism.
//
// Tx handling mirrors handleAcquireUnavailable: the per-candidate
// acquisition tx already rolled back via errAcquireProducerErrored; this
// function opens fresh transactions internally (the OnError dispatch
// path does its own tx work, and the partial-lock Abandon cleanup runs
// outside any rimsky-side tx).
//
//	@concept: error-policy
func handleAcquireProducerError(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
	// Defense-in-depth nil check: the tryAcquire path that returns
	// errAcquireProducerErrored always populates NodeDef (a producer
	// fault requires a ClaimSpec, which requires a template lookup).
	if acq.NodeDef == nil {
		return
	}
	// Abandon any claims that successfully Open'd before the fault was
	// hit. The tx-side rollback already removed the lock-holder rows;
	// the store-side Abandon undoes any producer-side state.
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	errorClass := acq.ProducerErrorClass
	if errorClass == "" {
		errorClass = producerAcquireErrorFallbackClass
	}
	if err := OnError(ctx, OnErrorArgs{
		Persist:      args.Persist,
		Queue:        args.Queue,
		Clock:        args.Clock,
		Logger:       args.Logger,
		NodeID:       cand.NodeID,
		RunScopeID:   acq.RunScopeID,
		InstanceID:   acq.InstanceID,
		SupervisorID: args.SupervisorID,
		ErrorClass:   errorClass,
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

// abandonPartialLocks calls Abandon on every already-Open'd ClaimSpec
// in the partial-acquired list. Mirrors handleOrphanedClaim's release
// branch (the tx-side rollback already removed the lock-holder rows).
//
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
