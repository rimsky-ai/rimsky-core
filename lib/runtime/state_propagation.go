// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type PropagationArgs struct {
	RunTree   persistence.RunTreeTable
	RunScopes persistence.RunScopeTable
}

func PropagateFromChildState(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	childRunID shared.UUID, newState cascade.NodeState, settlingSignalType *string,
) ([]CancelAction, []ParentSettlement, error) {
	if args.RunTree == nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: RunTree is required")
	}
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
				Changed:            true,
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
			return actions, settlements, nil
		}
		current = *parentScope.ParentRunID
	}
}

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

type CancelAction struct {
	ParentRunID shared.UUID
	Kind        AggregateAction
	Children    []persistence.RunTreeRow
}

func isSettled(state cascade.NodeState) bool {
	switch state {
	case cascade.NodeStateFresh, cascade.NodeStateFailed, cascade.NodeStateParked:
		return true
	}
	return false
}

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
	// @concept: run-scope — the run-tree shape lives on
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
