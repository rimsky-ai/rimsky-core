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
// SINGLE CARVE-OUT from the unified claim-handle resolution engine
// (`terminal_decision.go::ResolveClaimHandleTerminal`): every other
// path that fires a producer verb against an Open'd claim — the
// active-terminal, held-terminal, and ownership-bail sources — routes
// through that engine's audited verb-then-row-transition sequence.
// This path cannot: its acquisition tx has already rolled back, so
// the rimsky_claim_handles rows are gone and there is nothing for the
// engine to delete or promote — only the producer-side partial opens
// remain, Abandon'd via the shared helper (abandonPartialLocks below).
// Folding it in would require the engine to grow a no-rows mode,
// diluting its single audited verb-then-delete promise. Behavior is
// pinned by the deterministic injection test
// `test/scenarios/acquire_unavailable_abandon_injection_test.go`
// (abandon partial opens exactly once, no surviving rows, route via
// the producer-declared class else the synthetic acquisition class).
// See @concept: terminal-resolution.
//
//	@concept: error-policy
func handleAcquireUnavailable(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
	// Defense-in-depth nil check: today's tryAcquire path that returns
	// errAcquireUnavailable always populates NodeDef (an Unavailable
	// requires having a ClaimSpec, which requires a template lookup).
	if acq.NodeDef == nil {
		return
	}
	// Test-only seam: deterministically observe the post-rollback /
	// pre-Abandon window so an injection test can prove the tx-side
	// rows are gone before the producer-side Abandon fires, and that
	// the Abandon then fires exactly once. Nil in production (no
	// behavior change); see RunArgs.PreAcquireUnavailableHook.
	if args.PreAcquireUnavailableHook != nil {
		args.PreAcquireUnavailableHook(ctx)
	}
	// Abandon any claims that successfully Open'd before the Unavailable
	// was hit. The tx-side rollback already removed the lock-holder
	// rows; the store-side Abandon undoes any producer-side state.
	abandonPartialLocks(ctx, args, acq.PartialLocks)

	// Re-claim the dispatch row before resolving the policy: the
	// errAcquireUnavailable rollback undid the in-tx ClaimDispatchRow,
	// so the row sits pending+unclaimed and OnError's claimant-guarded
	// retire (RemoveForNodeInTx, blessed-invariant 4) would silently
	// no-op — leaking a pending run row that the upstream gate then
	// reads as a forever-in-flight sender, livelocking every subscribed
	// receiver. Losing the re-claim CAS means another supervisor now
	// owns the row; that supervisor's own acquisition attempt routes
	// the policy, so we must not double-resolve here.
	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireUnavailable") {
		return
	}

	// Route through the operator's error_types: chain. Key on the
	// producer-declared acquisition-failure class (e.g.
	// "pg/claim_unavailable") when the producer named one on its
	// Unavailable response; otherwise fall back to the synthetic
	// "acquire/unavailable". Threading the producer-declared leaf is what
	// makes an operator's `error_types: { pg/claim_unavailable: ... }`
	// policy match — and what lands the canonical
	// `terminal/error/pg/claim_unavailable` signal on the event log a
	// subscriber routes to. The policy lookup falls back from the exact
	// producer-declared class to the synthetic "acquire/unavailable"
	// family (PolicyFallbackClass) so a template that declares only the
	// generic policy still catches classified failures; see
	// lookupPolicy's fallback-order comment. Absent BOTH policies →
	// OnError resolves to give_up("unknown_error_class") (intentional
	// fail-fast; operators that want retry declare it explicitly).
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

	// Re-claim the dispatch row before resolving the policy — mirrors
	// handleAcquireUnavailable: the rollback undid the in-tx claim, and
	// OnError's claimant-guarded retire silently no-ops on an unclaimed
	// row, leaking it pending. Bail when another supervisor won the CAS
	// (it owns the resolution).
	if !reclaimDispatchRowShortTx(ctx, args, cand, "handleAcquireProducerError") {
		return
	}

	// Policy lookup falls back from the exact producer-declared class
	// (ErrorInfo.Reason) to the synthetic "acquire/producer_error"
	// family — mirroring handleAcquireUnavailable's fallback — so a
	// template that declares only the generic policy still catches
	// classified producer faults. See lookupPolicy's fallback-order
	// comment.
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

// reclaimDispatchRowShortTx re-claims the candidate's dispatch row in
// its own committed tx so the acquisition-failure policy resolution
// (OnError) operates on a row this supervisor owns. tryAcquire claims
// the row INSIDE the per-candidate tx; the errAcquireUnavailable /
// errAcquireProducerErrored rollback undoes that claim, leaving the row
// pending+unclaimed — and every claimant-guarded write OnError performs
// (retire, retry re-enqueue removal) keys on claimed_by = SupervisorID.
// Without the re-claim those writes silently match zero rows and the
// pending row leaks: it re-claims every poll, the strict policy-state
// machine rejects the now-settled node, and the in-flight upstream gate
// (runner_acquire_upstream_gate.go) reads the ghost row as a live
// sender, livelocking subscribed receivers.
//
// Returns false (after logging) when the CAS loses — either the row
// vanished (a concurrent resolution retired it) or another supervisor
// claimed it (that supervisor's own acquisition attempt routes the
// policy). The caller must skip its OnError dispatch in that case so
// the policy chain advances exactly once per occurrence.
//
// Load-bearing property protected here: the dispatch row is always
// retired (or re-enqueued by retry) in the same resolution that settles
// the node — no orphaned pending rows behind a settled run.
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

// abandonPartialLocks calls Abandon on every already-Open'd ClaimSpec
// in the partial-acquired list, via the shared abandonOpenedClaim
// helper. The tx-side rollback already removed the lock-holder rows,
// so no row delete follows (see the carve-out rationale on
// handleAcquireUnavailable above).
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
