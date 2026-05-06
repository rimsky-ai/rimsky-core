// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle-handler dispatch for on_executor_blocked / on_executor_errored.
// Split out of runner_terminal.go to keep that file under the cold-read
// 500-line guideline. The Complete-branch dispatch (on_executor_complete)
// stays in runner_terminal.go because it interleaves with attribute
// upsert + cascade fan-out and reads better in one place.
//
// Per .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md.

package integration

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// applyTerminalBlockedOrErrored consults the node's
// on_executor_blocked / on_executor_errored handler. If resolve=pass
// is declared, the supervisor abandons acquired claims, transitions
// the node to fresh+passed, and skips error_types routing entirely.
// If resolve=error is declared, the supervisor uses the configured
// error_class (or the executor-supplied class on errored) to route
// through error_types. Default (handler nil) preserves today's
// behavior — applyTerminalAppError with the executor-supplied class.
func applyTerminalBlockedOrErrored(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, terminalKind string,
) error {
	var handler *node.OnExecutorTerminalHandler
	if acq.NodeDef != nil {
		switch terminalKind {
		case "blocked":
			handler = acq.NodeDef.OnExecutorBlocked
		case "errored":
			handler = acq.NodeDef.OnExecutorErrored
		}
	}
	if handler == nil || handler.Resolve == "" {
		// Today's default: route through the executor-supplied class.
		return applyTerminalAppError(ctx, args, acq, errorClass, payload)
	}
	switch handler.Resolve {
	case node.ResolvePass:
		return applyTerminalPass(ctx, args, acq, errorClass, payload, terminalKind, handler)
	case node.ResolveError:
		// resolve=error overrides the executor-supplied class with the
		// handler's declared error_class. ErrorClass empty falls back
		// to the executor-supplied class — defensive; the validator
		// catches the missing-class case at deploy.
		routedClass := handler.ErrorClass
		if routedClass == "" {
			routedClass = errorClass
		}
		if err := applyTerminalAppError(ctx, args, acq, routedClass, payload); err != nil {
			return err
		}
		if handler.Invalidate != nil {
			frameID := acq.FrameID
			emitHandlerInvalidate(ctx, args, acq.NodeID, acq.NodeType, acq.InstanceID, &frameID, handler.Invalidate)
		}
		return nil
	}
	// Validator should have caught any other resolve value.
	return applyTerminalAppError(ctx, args, acq, errorClass, payload)
}

// applyTerminalPass executes resolve=pass on on_executor_blocked /
// on_executor_errored. Mirrors handleOrphanedClaim's Abandon-then-clear
// for the producer-side state, then transitions running → fresh with
// last_outcome=passed and skips error_types routing.
func applyTerminalPass(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, terminalKind string,
	handler *node.OnExecutorTerminalHandler,
) error {
	// Release acquired locks via the failure branch (Abandon for
	// non-held; mark + auto-terminal for held). Same path as
	// applyTerminalAppError takes today.
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := releaseLocksInTx(ctx, args, tx, acq, false); err != nil {
			return err
		}
		if err := args.Persist.Nodes().UpdateError(ctx, acq.NodeID, node.EvaluatorState{}, tx); err != nil {
			return err
		}
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			shared.NodeStateFresh, cascade.ReasonHandlerPass,
			shared.LastOutcomePassed, tx); err != nil {
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
	if handler.Invalidate != nil {
		frameID := acq.FrameID
		emitHandlerInvalidate(ctx, args, acq.NodeID, acq.NodeType, acq.InstanceID, &frameID, handler.Invalidate)
	}
	return nil
}
