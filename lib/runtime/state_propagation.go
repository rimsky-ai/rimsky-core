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
// @concept: run-scope
// @concept: terminal-resolution

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// parentSettlementSignal maps a propagated parent's new aggregated
// state + settling signal type-path to a synthetic terminal signal that
// the cascade walker can match subscribers against. Used by the
// run-tree propagation bridge when a child terminal forces a parent to
// settle. The aggregator computes the canonical type-path
// (terminal/error/aggregate/<policy>_failed for failures, terminal/success
// for success); this helper assembles the matching payload so receiver
// CEL predicates have well-shaped data to evaluate.
func parentSettlementSignal(state cascade.NodeState, sigType signalpkg.TypePath, changed bool) signalpkg.Signal {
	switch state {
	case cascade.NodeStateFailed:
		typ := sigType
		if typ == "" {
			typ = signalpkg.TypePath("terminal/error/aggregate/strict_failed")
		}
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"error_class":    string(typ)[len("terminal/error/"):],
				"error_payload":  map[string]any{},
				"attempt":        0,
				"retries_so_far": 0,
			},
		}
	case cascade.NodeStateParked:
		typ := sigType
		if typ == "" {
			typ = signalpkg.TypePath("terminal/park/snooze")
		}
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"parked_reason_label": "aggregated_park",
			},
		}
	default:
		typ := sigType
		if typ == "" {
			typ = signalpkg.TypePath("terminal/success")
		}
		return signalpkg.Signal{
			Type: typ,
			Payload: map[string]any{
				"changed":          changed,
				"attributes_delta": map[string]any{},
				"change_summary":   "aggregated_settlement",
			},
		}
	}
}

// PropagationArgs is the in-context dependencies the propagation
// transaction needs. RunTree handles the lock/read/write on
// rimsky_node_runs; RunScopes resolves the parent run id from a child
// run's RunScope (RunTreeRow no longer projects ParentRunID inline).
type PropagationArgs struct {
	RunTree   persistence.RunTreeTable
	RunScopes persistence.RunScopeTable
}

// PropagateFromChildState is the entry-point: a child run has just
// reached (newState, settlingSignalType) — the child-row write is the
// CALLER's responsibility (the terminal handler chain writes the row
// through `Queue.RemoveForNodeInTx` / `Queue.ParkActiveInTx` /
// `Nodes().UpdateState` before invoking this helper). This walker walks
// the run-tree upward, recomputing each ancestor's aggregated state,
// until reaching a non-terminal ancestor or the root. Operates inside
// the caller's tx so the propagation commits atomically with whatever
// else the caller is writing.
//
// The (newState, settlingSignalType) arguments are advisory — the
// walker reads the canonical state from the just-written
// rimsky_node_runs row via `ListChildren` on the parent. They're kept
// on the signature so the caller's terminal handler can stay
// structurally similar to the pre-rename PropagateChildState call site
// (and so callers that don't pre-write the row in the same tx can
// still drive the walker; tests that need that affordance must do
// their own write first). `settlingSignalType` may be nil for
// non-settling transitions.
//
// Returns:
//
//   - []CancelAction — follow-up sibling-cancellation work the caller
//     must walk (typically passed to ApplyCancelActions).
//   - []ParentSettlement — ancestors whose state transitioned to a
//     terminal value as a result of this child's terminal. The caller
//     fires cascadeSubscribersStaleInTx for each settlement to bridge
//     cross-scope cascade: a parent run's downstream subscribers (in
//     the parent's RunScope) must observe the parent's settlement, and
//     the walker is the only path that produces those settlements.
//     Without this bridge, fan-out / sub-graph parents that settle via
//     aggregation walks leave their main-scope subscribers ungated.
func PropagateFromChildState(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	childRunID shared.UUID, newState cascade.NodeState, settlingSignalType *string,
) ([]CancelAction, []ParentSettlement, error) {
	if args.RunTree == nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: RunTree is required")
	}
	// @deliberate: reference newState/settlingSignalType so callers'
	// intent shows up at the type level even though the walker re-reads
	// canonical rows below.
	_, _ = newState, settlingSignalType

	childRow, err := args.RunTree.GetByID(ctx, tx, childRunID)
	if err != nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: load child %s: %w", childRunID, err)
	}
	if childRow == nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: child run %s not found", childRunID)
	}
	if args.RunScopes == nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: RunScopes is required")
	}
	scope, err := args.RunScopes.GetByID(ctx, tx, childRow.RunScopeID)
	if err != nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: load run scope %s: %w", childRow.RunScopeID, err)
	}
	if scope == nil || scope.ParentRunID == nil {
		return nil, nil, nil
	}
	return walkUpwards(ctx, args, tx, *scope.ParentRunID)
}

// walkUpwards recurses up the tree, locking each ancestor in turn and
// applying the aggregation rule table. Stops when the ancestor's state
// did not change (no further propagation needed) or the root is updated.
//
// Settlements (parents whose state transitioned to a terminal value via
// the aggregation walker) are accumulated and returned alongside cancel
// actions. The caller fires cascadeSubscribersStaleInTx for each
// settled parent so the parent's own downstream subscribers (in the
// parent's RunScope) observe the settlement — this is the only path
// that bridges fan-out / sub-graph parent settlement into the standard
// subscription cascade.
func walkUpwards(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	parentRunID shared.UUID,
) ([]CancelAction, []ParentSettlement, error) {
	var actions []CancelAction
	var settlements []ParentSettlement
	current := parentRunID
	for {
		parent, err := args.RunTree.LockTreeForUpdate(ctx, tx, current)
		if err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: lock %s: %w", current, err)
		}
		if parent == nil {
			return actions, settlements, fmt.Errorf("walkUpwards: parent %s not found", current)
		}
		children, err := args.RunTree.ListChildren(ctx, tx, current)
		if err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: list children %s: %w", current, err)
		}
		// @concept: signal — each child's settling signal type +
		// payload.changed is projected forward into the pure aggregation
		// function's input for the parent aggregator.
		inputs := make([]ChildState, len(children))
		for i, c := range children {
			var sigType signalpkg.TypePath
			if c.SettlingSignalType != nil {
				sigType = signalpkg.TypePath(*c.SettlingSignalType)
			}
			inputs[i] = ChildState{
				State:              c.State,
				SettlingSignalType: sigType,
				// @deliberate: `changed` is not projected from the
				// persistence row today (the signal payload lives only in
				// rimsky_events). Defaulting to true preserves the
				// pre-Pass-5 behavior where settled-success children
				// invariably propagated fresh_changed upward; downstream
				// subscribers that genuinely care about `changed` can
				// subscribe to the child's own `terminal/success` signal
				// directly.
				Changed: true,
			}
		}
		result := Aggregate(inputs, parent.AggregationPolicy)
		if !result.IsSettled {
			return actions, settlements, nil
		}
		var parentSig string
		if parent.SettlingSignalType != nil {
			parentSig = *parent.SettlingSignalType
		}
		if parent.State == result.ParentState && parentSig == string(result.ParentSettlingSignalType) {
			return actions, settlements, nil
		}
		// @constraint: validate the parent transition via
		// NextStateParent. The machine returns a parentAggregateOK
		// sentinel when the caller (us) chose the target via aggregation
		// — that's the expected shape for child_transitioned. Any other
		// error is illegal.
		if _, err := cascade.NextStateParent(parent.State, cascade.ReasonChildTransitioned); err != nil {
			if !cascade.IsParentAggregateOK(err) {
				return actions, settlements, fmt.Errorf("walkUpwards: state-machine rejects parent %s %s→%s: %w",
					current, parent.State, result.ParentState, err)
			}
		}
		newSig := string(result.ParentSettlingSignalType)
		var newSigArg *string
		if newSig != "" {
			newSigArg = &newSig
		}
		if err := args.RunTree.UpdateStateAndOutcome(ctx, tx, current, result.ParentState, newSigArg); err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: update parent %s: %w", current, err)
		}
		if result.Action != AggregateActionNone {
			actions = append(actions, CancelAction{
				ParentRunID: current,
				Kind:        result.Action,
				Children:    children,
			})
		}
		// @constraint: record terminal-state settlement so the caller
		// can fire cascadeSubscribersStaleInTx for the parent's
		// downstream subscribers. NodeID + FrameID + RunScopeID come
		// from the parent's just-locked row; the caller resolves
		// NodeType from the node row before invoking the cascade walker.
		if isSettled(result.ParentState) {
			settlements = append(settlements, ParentSettlement{
				ParentRunID:           current,
				ParentNodeID:          parent.NodeID,
				ParentRunScope:        parent.RunScopeID,
				FrameID:               parent.FrameID,
				NewState:              result.ParentState,
				NewSettlingSignalType: result.ParentSettlingSignalType,
				NewChanged:            result.ParentChanged,
			})
		}
		if !isSettled(result.ParentState) {
			return actions, settlements, nil
		}
		// @concept: run-scope — walk further up via the parent's
		// RunScope; RunTreeRow no longer projects parent_run_id inline,
		// the tree shape lives on rimsky_run_scopes.
		parentScope, err := args.RunScopes.GetByID(ctx, tx, parent.RunScopeID)
		if err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: load run scope %s: %w", parent.RunScopeID, err)
		}
		if parentScope == nil || parentScope.ParentRunID == nil {
			// @constraint: reached root (main RunScope) — further
			// upward is the frame's terminal-aggregation engine, not
			// handled here.
			return actions, settlements, nil
		}
		current = *parentScope.ParentRunID
	}
}

// ParentSettlement describes a parent run that transitioned to a
// settled state during a propagation walk. The caller (post-terminal
// handler) uses this to fire cascadeSubscribersStaleInTx for the
// parent's own downstream subscribers — the cross-scope cascade bridge
// from fan-out / sub-graph parent settlement into the main-graph
// subscription cascade.
//
// @concept: cascade
// @concept: run-scope
type ParentSettlement struct {
	ParentRunID           shared.UUID
	ParentNodeID          shared.UUID
	ParentRunScope        shared.UUID
	FrameID               shared.UUID
	NewState              cascade.NodeState
	NewSettlingSignalType signalpkg.TypePath
	NewChanged            bool
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

// isSettled reports whether a NodeState is a settled state (fresh,
// failed, parked).
//
// Renamed from isTerminal in Pass 5 of spec
// 2026-05-23-signal-taxonomy-and-policy-decoupling-design: "terminal"
// in this codebase refers to the wire-protocol StreamClose envelope
// (concept:terminal-resolution); the state-machine landing predicate
// is "settled" — distinct vocabulary.
func isSettled(state cascade.NodeState) bool {
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
// `settlingSignalType` is the canonical signal type-path the child run
// settled with (concept:signal); nil for non-settling transitions
// (advisory only — walker re-reads canonical rows).
//
// Returned CancelActions are surfaced for the caller to apply
// (sibling cancellation walks). At the V1 wiring posture the runner
// logs them but does not yet drive the per-sibling Abandon — see the
// follow-up tension in `.ok-planner/specs/2026-05-15-data-platform-
// extensions-design.md §State propagation transaction`.
func PropagateIfChildAfterTerminal(
	ctx context.Context, args RunArgs,
	runID shared.UUID, newState cascade.NodeState, settlingSignalType *string,
) ([]CancelAction, error) {
	if args.Persist == nil {
		return nil, nil
	}
	rt := args.Persist.RunTree()
	if rt == nil {
		return nil, nil
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return nil, nil
	}
	// @deliberate: cheap pre-check — only children need propagation; a
	// root run's terminal is the frame-engine's concern.
	// @concept: run-scope — the run-tree shape lives on
	// rimsky_run_scopes, so resolve the child's RunScope and check
	// whether it has a parent_run_id.
	var parentID *shared.UUID
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := rt.GetByID(ctx, tx, runID)
		if err != nil || row == nil {
			return err
		}
		scope, err := scopes.GetByID(ctx, tx, row.RunScopeID)
		if err != nil || scope == nil {
			return err
		}
		parentID = scope.ParentRunID
		return nil
	}); err != nil {
		return nil, err
	}
	if parentID == nil {
		return nil, nil
	}
	var actions []CancelAction
	var settlements []ParentSettlement
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		outActions, outSettlements, err := PropagateFromChildState(ctx, PropagationArgs{
			RunTree:   rt,
			RunScopes: scopes,
		}, tx, runID, newState, settlingSignalType)
		actions = outActions
		settlements = outSettlements
		if err != nil {
			return err
		}
		// @concept: cascade
		// @concept: run-scope
		// @constraint: cross-scope cascade bridge — for each parent
		// that settled to a terminal state via aggregation, fire the
		// standard subscription cascade walker rooted at the parent's
		// run. Without this, fan-out / sub-graph parents that settle
		// here leave their main-scope subscribers ungated; the parent
		// never went through a terminal handler that would naturally
		// fire the walker.
		for _, s := range settlements {
			if s.FrameID == (shared.UUID{}) {
				// @deliberate: no frame projected on the parent row
				// means no current-frame cascade target. Skip rather
				// than faulting; this is the orchestration-only-parent
				// case. Log Warn so a degenerate case (e.g. parent row
				// was created but frame_id never bound) is observable
				// in logs rather than silently stranding the parent's
				// downstream subscribers. Fan-out / sub-graph parents
				// normally always carry a frame_id, so reaching here
				// signals a logic gap worth surfacing.
				if args.Logger != nil {
					args.Logger.Warn("PropagateIfChildAfterTerminal: skip cascade bridge: parent frame_id is zero",
						"parent_run_id", s.ParentRunID.String(),
						"parent_node_id", s.ParentNodeID.String(),
						"new_state", string(s.NewState),
						"settling_signal_type", string(s.NewSettlingSignalType))
				}
				continue
			}
			nodeRow, err := args.Persist.Nodes().Get(ctx, s.ParentNodeID, tx)
			if err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: load parent node %s: %w", s.ParentNodeID, err)
			}
			if nodeRow == nil {
				continue
			}
			// @concept: signal — synthesize a parent-settlement signal
			// so the cascade walker can apply subscriber CEL
			// predicates. The parent's settling signal-type from the
			// aggregator drives the envelope; payload.changed comes
			// from the aggregator's projected ParentChanged.
			parentSig := parentSettlementSignal(s.NewState, s.NewSettlingSignalType, s.NewChanged)
			if err := cascadeSubscribersStaleInTx(
				ctx, args, tx,
				s.ParentNodeID,
				nodeRow.NodeType,
				s.ParentRunID,
				nodeRow.InstanceID,
				s.FrameID,
				parentSig,
			); err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: cascade parent %s: %w", s.ParentRunID, err)
			}
			// @concept: wait-set
			// @constraint: drain wait-set rows where the just-settled
			// parent is the gating sender. The cascade walker above
			// inserts rows keyed on (sender=parent_run_id,
			// receiver=downstream); without the drain, those rows stay
			// blocking forever — downstream pending receivers never
			// advance to dispatch. Mirrors the standard
			// applyTerminalComplete pattern (cascade then drain) for
			// the cross-scope bridge.
			if err := args.Persist.WaitSet().MarkDrainedBySender(ctx, s.FrameID, s.ParentRunID, tx); err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: drain wait-set for parent %s: %w", s.ParentRunID, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return actions, nil
}
