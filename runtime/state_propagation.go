// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state_propagation.go — E2. Run-tree state propagation transaction.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §State propagation transaction. When a child run terminates, the
// supervisor:
//
//  1. Locks the parent's row (SELECT ... FOR UPDATE).
//  2. Re-evaluates aggregation against all children's current states.
//  3. Computes the parent's new state.
//  4. If state changed, writes the parent's new state with reason
//     ReasonChildTransitioned.
//  5. If the parent's new state is terminal, repeats steps 1-4 for the
//     grandparent.
//  6. Continues up the tree until a non-terminal ancestor is reached or
//     the root is updated.
//
// Single transaction; ancestor locks taken in tree order (always upward)
// — avoids deadlock since the partial order is a tree.
//
// @concept: run-tree
// @concept: aggregation-policy

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// PropagationArgs is the in-context dependencies the propagation
// transaction needs. RunTree handles the lock/read/write on
// rimsky_node_runs; ClaimHandles is required so that strict.cancel_siblings
// follow-up actions can walk the affected siblings' claim handles. Logger
// is for audit lines; nil disables.
type PropagationArgs struct {
	RunTree      persistence.RunTreeTable
	ClaimHandles persistence.ClaimHandleTable
	Logger       shared.Logger
}

// PropagateFromChildState is the entry-point: a child run has just
// reached (newState, newOutcome) — the child-row write is the CALLER's
// responsibility (the terminal handler chain writes the row through
// `Queue.RemoveForNodeInTx` / `Queue.ParkActiveInTx` / `Nodes().UpdateState`
// before invoking this helper). This walker walks the run-tree upward,
// recomputing each ancestor's aggregated state, until reaching a
// non-terminal ancestor or the root. Operates inside the caller's tx so
// the propagation commits atomically with whatever else the caller is
// writing.
//
// The (newState, newOutcome) arguments are advisory — the walker reads
// the canonical state from the just-written rimsky_node_runs row via
// `ListChildren` on the parent. They're kept on the signature so the
// caller's terminal handler can stay structurally similar to the
// pre-rename PropagateChildState call site (and so callers that don't
// pre-write the row in the same tx can still drive the walker; tests
// that need that affordance must do their own write first).
//
// Returns the list of cancellation actions the caller must execute
// (typically alongside the surrounding transaction). Callers may pass
// the result to ApplyCancelActions to walk siblings' claim handles.
func PropagateFromChildState(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	childRunID shared.UUID, newState cascade.NodeState, newOutcome cascade.LastOutcome,
) ([]CancelAction, error) {
	if args.RunTree == nil {
		return nil, fmt.Errorf("PropagateFromChildState: RunTree is required")
	}
	// Reference the args/state values so callers' intent shows up at the
	// type level even though the walker re-reads canonical rows below.
	_, _ = newState, newOutcome

	childRow, err := args.RunTree.GetByID(ctx, tx, childRunID)
	if err != nil {
		return nil, fmt.Errorf("PropagateFromChildState: load child %s: %w", childRunID, err)
	}
	if childRow == nil {
		return nil, fmt.Errorf("PropagateFromChildState: child run %s not found", childRunID)
	}
	if childRow.ParentRunID == nil {
		// Root run — no further propagation.
		return nil, nil
	}
	return walkUpwards(ctx, args, tx, *childRow.ParentRunID)
}

// walkUpwards recurses up the tree, locking each ancestor in turn and
// applying the aggregation rule table. Stops when the ancestor's state
// did not change (no further propagation needed) or the root is updated.
func walkUpwards(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	parentRunID shared.UUID,
) ([]CancelAction, error) {
	var actions []CancelAction
	current := parentRunID
	for {
		parent, err := args.RunTree.LockTreeForUpdate(ctx, tx, current)
		if err != nil {
			return actions, fmt.Errorf("walkUpwards: lock %s: %w", current, err)
		}
		if parent == nil {
			return actions, fmt.Errorf("walkUpwards: parent %s not found", current)
		}
		children, err := args.RunTree.ListChildren(ctx, tx, current)
		if err != nil {
			return actions, fmt.Errorf("walkUpwards: list children %s: %w", current, err)
		}
		// Translate children rows into the pure aggregation function's
		// input.
		inputs := make([]ChildState, len(children))
		for i, c := range children {
			inputs[i] = ChildState{State: c.State, LastOutcome: c.LastOutcome}
		}
		result := Aggregate(inputs, parent.AggregationPolicy)
		if !result.IsTerminal {
			// Parent stays in its current state. Nothing more to do
			// upward.
			return actions, nil
		}
		if parent.State == result.ParentState && parent.LastOutcome == result.ParentOutcome {
			// State unchanged — propagation halts.
			return actions, nil
		}
		// Validate the parent transition via NextStateParent. The
		// machine returns a parentAggregateOK sentinel when the caller
		// (us) chose the target via aggregation — that's the expected
		// shape for child_transitioned. Any other error is illegal.
		if _, err := cascade.NextStateParent(parent.State, cascade.ReasonChildTransitioned); err != nil {
			if !cascade.IsParentAggregateOK(err) {
				return actions, fmt.Errorf("walkUpwards: state-machine rejects parent %s %s→%s: %w",
					current, parent.State, result.ParentState, err)
			}
		}
		if err := args.RunTree.UpdateStateAndOutcome(ctx, tx, current, result.ParentState, result.ParentOutcome); err != nil {
			return actions, fmt.Errorf("walkUpwards: update parent %s: %w", current, err)
		}
		// Record any follow-up action.
		if result.Action != AggregateActionNone {
			actions = append(actions, CancelAction{
				ParentRunID: current,
				Kind:        result.Action,
				Children:    children,
			})
		}
		if !isTerminal(result.ParentState) {
			// Parent still active (e.g. policy left it running with
			// stale children) — no further propagation.
			return actions, nil
		}
		if parent.ParentRunID == nil {
			// Reached root; further upward is just the frame's
			// terminal-aggregation engine (not handled here).
			return actions, nil
		}
		current = *parent.ParentRunID
	}
}

// CancelAction describes a follow-up cancellation the propagation
// transaction produced. Callers (typically the supervisor's terminal
// handler) walk the children list and apply the per-child cancellation
// (Abandon claim handles, transition siblings to failed{error_class:
// "sibling_failed"}).
//
// Surfaced as a return value so the persistence-layer code path stays
// free of cross-feature imports (claim-handle resolution lives in
// runtime/auto_terminal.go::CheckAndFireResolution; siblings transition
// via the standard runner_terminal path). The supervisor wires them
// together.
type CancelAction struct {
	ParentRunID shared.UUID
	Kind        AggregateAction
	Children    []persistence.RunTreeRow
}

// isTerminal reports whether a NodeState is a settled terminal state
// (fresh, failed, parked).
func isTerminal(state cascade.NodeState) bool {
	switch state {
	case cascade.NodeStateFresh, cascade.NodeStateFailed, cascade.NodeStateParked:
		return true
	}
	return false
}

// PropagateIfChildAfterTerminal is the runtime-side hook the
// post-terminal handlers (applyTerminalComplete / applyTerminalPark /
// applyErrorPolicy give_up) call to drive aggregation up the run-tree.
// It is a no-op when the run is a root (parent_run_id IS NULL), so the
// caller can invoke it unconditionally at every terminal.
//
// `args.Persist` and `args.ClaimHandles` are threaded so the helper
// reuses the runner's wiring rather than reaching for a separate set
// of accessors.
//
// The propagation runs in its own transaction. Callers fire it AFTER
// the terminal-state write tx commits — the propagation reads the
// child's just-committed state from the database, walks upward locking
// each ancestor, and writes parent-side state changes per the
// aggregation policy.
//
// Returned CancelActions are surfaced for the caller to apply
// (sibling cancellation walks). At the V1 wiring posture the runner
// logs them but does not yet drive the per-sibling Abandon — see the
// follow-up tension in `.ok-planner/specs/2026-05-15-data-platform-
// extensions-design.md §State propagation transaction`.
func PropagateIfChildAfterTerminal(
	ctx context.Context, args RunArgs,
	runID shared.UUID, newState cascade.NodeState, newOutcome cascade.LastOutcome,
) ([]CancelAction, error) {
	if args.Persist == nil {
		return nil, nil
	}
	rt := args.Persist.RunTree()
	if rt == nil {
		return nil, nil
	}
	// Cheap pre-check: only children need propagation. A root run's
	// terminal is the frame-engine's concern.
	var parentID *shared.UUID
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := rt.GetByID(ctx, tx, runID)
		if err != nil || row == nil {
			return err
		}
		parentID = row.ParentRunID
		return nil
	}); err != nil {
		return nil, err
	}
	if parentID == nil {
		return nil, nil
	}
	var actions []CancelAction
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := PropagateFromChildState(ctx, PropagationArgs{
			RunTree:      rt,
			ClaimHandles: args.ClaimHandles,
			Logger:       args.Logger,
		}, tx, runID, newState, newOutcome)
		actions = out
		return err
	}); err != nil {
		return nil, err
	}
	return actions, nil
}
