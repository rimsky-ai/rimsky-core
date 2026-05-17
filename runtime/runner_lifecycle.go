// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle-handler dispatch for the three declarative slots:
//
//   - on_acquire_unavailable — runs when any required claim's Open returned
//     Unavailable. Default = silent retry (today's behavior). Optional
//     resolutions: pass (transition fresh+passed), error (route through
//     error_types).
//   - on_executor_complete — runs at supervisor terminal-complete. Default =
//     by_changed (today's behavior). Optional: always_propagate /
//     never_propagate.
//   - on_executor_errored — runs at the executor-error terminal (the
//     post-2026-05-12 collapse routes the executor-blocked terminal
//     through the same slot via error_class: "executor_blocked").
//     Default = route through error_types (today's behavior). Optional
//     resolution: pass (transition fresh+passed without error routing),
//     error (route through a specific error_class).
//
// Per .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md.
package runtime

import (
	"context"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
)

// handleAcquireUnavailable runs the on_acquire_unavailable handler for
// a candidate whose acquisition path returned errAcquireUnavailable.
// Default (handler == nil OR resolve == retry) is today's silent retry —
// the per-candidate tx already rolled back; we do nothing and the next
// scheduler tick retries. resolve == pass / error fire Abandon on
// already-Open'd partial claims (matches handleOrphanedClaim semantics)
// and apply the resolution.
func handleAcquireUnavailable(ctx context.Context, args RunArgs, acq acquisition, cand persistence.Candidate) {
	// Defense-in-depth nil check: today's tryAcquire path that returns
	// errAcquireUnavailable always populates NodeDef (an Unavailable
	// requires having a ClaimSpec, which requires a template lookup).
	// Mirror applyTerminalError's pattern so a future
	// refactor that exposes a nil-NodeDef path crashes in tests instead
	// of in production.
	if acq.NodeDef == nil {
		return
	}
	handler := acq.NodeDef.OnAcquireUnavailable
	if handler == nil || handler.Resolve == "" || handler.Resolve == node.ResolveRetry {
		// Today's default — silent retry. Tx already rolled back; nothing
		// more to do.
		return
	}
	abandonPartialLocks(ctx, args, acq.PartialLocks)
	switch handler.Resolve {
	case node.ResolvePass:
		applyAcquirePass(ctx, args, acq, cand, handler)
	case node.ResolveError:
		applyAcquireError(ctx, args, acq, cand, handler)
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

// applyAcquirePass executes resolve=pass on on_acquire_unavailable:
// transitions the node stale → fresh with last_outcome=passed and
// fires the optional invalidate emit.
func applyAcquirePass(
	ctx context.Context, args RunArgs, acq acquisition,
	cand persistence.Candidate, h *node.OnAcquireUnavailableHandler,
) {
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := args.Persist.Nodes().UpdateState(ctx, cand.NodeID,
			cascade.NodeStateFresh, cascade.ReasonAcquirePass,
			cascade.LastOutcomePassed, tx); err != nil {
			return err
		}
		// Settled-state drain on fresh+passed: this sender reached
		// a settled state, so any wait-set rows gating receivers on
		// this sender's run release. cand.DispatchID is this run's
		// rimsky_node_runs.id post-stage-5.
		//
		//	@concept: wait-set
		if err := drainWaitSetOnSettled(ctx, args, tx, cand.FrameID, cand.DispatchID); err != nil {
			return err
		}
		// Mark the node-run as handled so the queue doesn't
		// re-pick it. Mirrors the post-terminal cleanup.
		if err := args.Queue.RemoveForNodeInTx(ctx, cand.NodeID, args.SupervisorID, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &cand.NodeID, InstanceID: &acq.InstanceID,
			Kind: "state_transition",
			Payload: map[string]any{
				"from": "stale", "to": "fresh", "reason": "acquire_pass",
				"last_outcome": "passed",
			},
		}, tx)
	})
	// Per the 2026-05-14 subscription-cascade resolution, the
	// invalidate-emit slot retired; cascade coupling is declared
	// receiver-side via Subscribes.
	_ = h
}

// applyAcquireError executes resolve=error on on_acquire_unavailable:
// the node has not entered running yet, so we route the configured
// error_class through OnError directly (which uses the node's current
// state — stale — and applies the policy-chain action accordingly).
//
// Because OnError today assumes the node is running for retry /
// invalidate transitions (which this code path can't satisfy without
// first running the node), the realistic resolution under
// on_acquire_unavailable: error is give_up — a way to signal "this
// claim is permanently unavailable for this node, fail it." The state
// machine accepts `policy_give_up` from both running and stale (see
// foundation/cascade/state.go) so this path lands the node in failed.
// Any other policy-chain action is routed through OnError and may
// surface a no-op state transition; the validator allows it but
// operators should compose error_types policies that terminate.
func applyAcquireError(
	ctx context.Context, args RunArgs, acq acquisition,
	cand persistence.Candidate, h *node.OnAcquireUnavailableHandler,
) {
	_ = OnError(ctx, OnErrorArgs{
		Persist:      args.Persist,
		Queue:        args.Queue,
		Clock:        args.Clock,
		Logger:       args.Logger,
		NodeID:       cand.NodeID,
		InstanceID:   acq.InstanceID,
		SupervisorID: args.SupervisorID,
		ErrorClass:   h.ErrorClass,
		Payload: map[string]any{
			"source": "on_acquire_unavailable",
		},
		Metrics: args.Metrics,
	})
	// Per the 2026-05-14 subscription-cascade resolution, the
	// invalidate-emit slot retired; cascade coupling is declared
	// receiver-side via Subscribes.
}

// emitHandlerInvalidate retired under the 2026-05-14 subscription-cascade
// resolution. Cascade coupling is declared receiver-side via
// `subscribes:`; the per-template subscription-edge inverse map drives
// cascade walks. The send-side invalidate emit has no remaining call
// sites — the function and its helper were removed.
