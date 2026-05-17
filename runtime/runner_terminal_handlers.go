// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle-handler dispatch for on_executor_errored. Split out of
// runner_terminal.go to keep that file under the cold-read 500-line
// guideline. The Success-outcome-branch dispatch
// (on_executor_complete) stays in runner_terminal.go because it
// interleaves with attribute upsert + cascade fan-out and reads better
// in one place.
//
// Per .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md
// + .ok-planner/specs/2026-05-12-nomenclature-resolution-design.md §E.2 /
// §E.9 (Blocked collapsed into Error; on_executor_blocked slot retired).

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/graph/node"
)

// applyTerminalError consults the node's on_executor_errored handler.
// If resolve=pass is declared, the supervisor abandons acquired
// claims, transitions the node to fresh+passed, and skips error_types
// routing entirely. If resolve=error is declared, the supervisor uses
// the configured error_class (or the executor-supplied class) to route
// through error_types. Default (handler nil) preserves today's
// behavior — applyErrorPolicy with the executor-supplied class.
//
// Per the 2026-05-12 nomenclature resolution (E.2/E.9): the wire
// `Blocked` terminal collapsed into `Error{error_class: "executor_blocked"}`,
// so on_executor_errored is the sole error path; the `executor_blocked`
// error_class lets operator-side `error_types:` policy continue to
// distinguish blocked-style errors from other errors.
func applyTerminalError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any,
) error {
	var handler *node.OnExecutorTerminalHandler
	if acq.NodeDef != nil {
		handler = acq.NodeDef.OnExecutorErrored
	}
	if handler == nil || handler.Resolve == "" {
		// Today's default: route through the executor-supplied class.
		return applyErrorPolicy(ctx, args, acq, errorClass, payload)
	}
	switch handler.Resolve {
	case node.ResolvePass:
		return applyTerminalPass(ctx, args, acq, errorClass, payload, "errored", handler)
	case node.ResolveError:
		// resolve=error overrides the executor-supplied class with the
		// handler's declared error_class. ErrorClass empty falls back
		// to the executor-supplied class — defensive; the validator
		// catches the missing-class case at deploy.
		routedClass := handler.ErrorClass
		if routedClass == "" {
			routedClass = errorClass
		}
		if err := applyErrorPolicy(ctx, args, acq, routedClass, payload); err != nil {
			return err
		}
		// Per the 2026-05-14 subscription-cascade resolution, the
		// invalidate-emit slot retired; cascade coupling is declared
		// receiver-side via Subscribes.
		return nil
	}
	// Validator should have caught any other resolve value.
	return applyErrorPolicy(ctx, args, acq, errorClass, payload)
}

// applyTerminalPass executes resolve=pass on on_executor_errored.
// Mirrors handleOrphanedClaim's Abandon-then-clear for the
// producer-side state, then transitions running → fresh with
// last_outcome=passed and skips error_types routing.
func applyTerminalPass(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, terminalKind string,
	handler *node.OnExecutorTerminalHandler,
) error {
	// Release acquired locks via the failure branch (Abandon for
	// non-held; mark + auto-terminal for held). Same path as
	// applyErrorPolicy takes today.
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
			return err
		}
		if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
			return err
		}
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			cascade.NodeStateFresh, cascade.ReasonHandlerPass,
			cascade.LastOutcomePassed, tx); err != nil {
			return err
		}
		// Settled-state drain on fresh+passed: this sender reached a
		// settled state, so any wait-set rows gating receivers on
		// this sender release. Mirrors applyTerminalComplete's drain
		// at running → fresh.
		//
		//	@concept: wait-set
		if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
			return err
		}
		return args.Queue.RemoveForNodeInTx(ctx, acq.NodeID, args.SupervisorID, tx)
	}); err != nil {
		return fmt.Errorf("applyTerminalPass: %w", err)
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "state_transition",
			Payload: map[string]any{
				"from": "running", "to": "fresh", "reason": "handler_pass",
				"last_outcome":  "passed",
				"terminal_kind": terminalKind,
				"error_class":   errorClass,
				"details":       payload,
			},
		}, tx)
	})
	// E8: emit leaf-run lineage record for the passed terminal.
	EmitLeafRunLineage(ctx, args,
		acq.InstanceID, acq.FrameID, acq.DispatchID, acq.NodeID, "",
		string(cascade.NodeStateFresh), string(cascade.LastOutcomePassed), errorClass,
		acq.InstanceParams, acq.InstanceUserdataOverrides)
	// Run-tree state propagation (E2): pass settles the run as
	// fresh+passed, so child→parent aggregation must fire if this run is
	// itself a child.
	if _, err := PropagateIfChildAfterTerminal(ctx, args, acq.DispatchID,
		cascade.NodeStateFresh, cascade.LastOutcomePassed); err != nil {
		args.Logger.Warn("applyTerminalPass: run-tree propagation failed",
			"run_id", acq.DispatchID.String(), "error", err.Error())
	}
	// Per the 2026-05-14 subscription-cascade resolution, the
	// invalidate-emit slot retired; cascade coupling is declared
	// receiver-side via Subscribes.
	_ = handler
	return nil
}
