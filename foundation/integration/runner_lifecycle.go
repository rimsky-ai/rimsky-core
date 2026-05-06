// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle-handler dispatch for the four declarative slots:
//
//   - on_acquire_unavailable — runs when any required claim's Open returned
//     Unavailable. Default = silent retry (today's behavior). Optional
//     resolutions: pass (transition fresh+passed), error (route through
//     error_types).
//   - on_executor_complete — runs at supervisor terminal-complete. Default =
//     by_changed (today's behavior). Optional: always_propagate /
//     never_propagate.
//   - on_executor_blocked / on_executor_errored — run at the corresponding
//     terminal. Default = route through error_types (today's behavior).
//     Optional resolution: pass (transition fresh+passed without error
//     routing), error (route through a specific error_class).
//
// Per .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md.
package integration

import (
	"context"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
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
	// Mirror applyTerminalBlockedOrErrored's pattern so a future
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
func abandonPartialLocks(ctx context.Context, args RunArgs, partial []AcquiredLock) {
	for _, lk := range partial {
		if lk.Store == nil {
			continue
		}
		scope := claimScope(lk)
		address := claimAddress(lk)
		claimID := locks.ClaimID(lk.LockHolderID.String())
		if err := lk.Store.Abandon(ctx, claimID, scope, address); err != nil {
			args.Logger.Warn("handleAcquireUnavailable: Abandon failed",
				"store", storeNameForSpec(lk.Spec), "error", err.Error())
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
			shared.NodeStateFresh, cascade.ReasonAcquirePass,
			shared.LastOutcomePassed, tx); err != nil {
			return err
		}
		// Mark the worker-request as handled so the queue doesn't
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
	if h.Invalidate != nil {
		frameID := acq.FrameID
		emitHandlerInvalidate(ctx, args, cand.NodeID, cand.NodeType, acq.InstanceID, &frameID, h.Invalidate)
	}
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
	})
	if h.Invalidate != nil {
		frameID := acq.FrameID
		emitHandlerInvalidate(ctx, args, cand.NodeID, cand.NodeType, acq.InstanceID, &frameID, h.Invalidate)
	}
}

// emitHandlerInvalidate resolves a HandlerInvalidate's Targets to
// node UUIDs within the instance (with `self` resolving to the source
// node's type) and fires InvalidateNode per Frame (default FrameNext).
//
// srcFrameID, when non-nil, is forwarded as InvalidateArgs.SourceFrameID
// so frame=in can land on the source's frame even if the caller's tx
// already cleared the source node row's frame_id (the post-Complete
// path; the running-tx commits state→fresh which clears frame_id, then
// fires this emit).
func emitHandlerInvalidate(
	ctx context.Context, args RunArgs,
	srcNodeID shared.UUID, srcNodeType string, instanceID shared.UUID,
	srcFrameID *shared.UUID, inv *node.HandlerInvalidate,
) {
	targets := resolveHandlerTargets(ctx, args, srcNodeID, instanceID, srcNodeType, inv.Targets)
	useFrame := inv.Frame
	if useFrame == "" {
		useFrame = node.FrameNext
	}
	src := srcNodeID
	for _, tid := range targets {
		_ = InvalidateNode(ctx, InvalidateArgs{
			Persist:       args.Persist,
			Queue:         args.Queue,
			Clock:         args.Clock,
			Logger:        args.Logger,
			SourceNodeID:  &src,
			SourceFrameID: srcFrameID,
			TargetNodeID:  tid,
			Reason:        "handler_invalidate",
			SupervisorID:  args.SupervisorID,
			Frame:         useFrame,
		})
	}
}

// resolveHandlerTargets walks the instance's nodes and resolves each
// target string to a node UUID. The literal "self" resolves to the
// source node's type. Unknown targets emit an
// `unresolved_invalidate_target` event for parity with
// invalidateTargets (the error_types policy-chain path). The event is
// keyed on srcNodeID so audit logs attribute the unresolved target to
// the node that fired the emit.
func resolveHandlerTargets(
	ctx context.Context, args RunArgs, srcNodeID shared.UUID,
	instanceID shared.UUID,
	srcNodeType string, targets []string,
) []shared.UUID {
	var rows []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := args.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
		rows = out
		return err
	}); err != nil {
		args.Logger.Warn("resolveHandlerTargets: ListByInstance failed",
			"instance_id", instanceID.String(), "error", err.Error())
		return nil
	}
	typeToID := make(map[string]shared.UUID, len(rows))
	for _, r := range rows {
		typeToID[r.NodeType] = r.ID
	}
	out := make([]shared.UUID, 0, len(targets))
	var unresolved []string
	for _, t := range targets {
		switch t {
		case node.SelfTarget:
			if id, ok := typeToID[srcNodeType]; ok {
				out = append(out, id)
			} else {
				unresolved = append(unresolved, t)
			}
		default:
			if id, ok := typeToID[t]; ok {
				out = append(out, id)
			} else {
				unresolved = append(unresolved, t)
				args.Logger.Warn("emitHandlerInvalidate: unresolved target",
					"target", t, "instance_id", instanceID.String(),
					"source_node_type", srcNodeType)
			}
		}
	}
	if len(unresolved) > 0 {
		nodeID := srcNodeID
		instID := instanceID
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &nodeID, InstanceID: &instID,
				Kind: "unresolved_invalidate_target",
				Payload: map[string]any{
					"unresolved_targets": unresolved,
					"source":             "handler_invalidate",
					"source_node_type":   srcNodeType,
				},
			}, tx)
		})
	}
	return out
}
