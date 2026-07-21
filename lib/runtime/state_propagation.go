// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope
// @concept: terminal-resolution

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
)

func parentSettlementSignal(state cascade.NodeState, sigType signalpkg.TypePath, changed bool) signalpkg.Signal {
	switch state {
	case cascade.NodeStateFailed:
		errorClass := strings.TrimPrefix(string(sigType), "terminal/error/")
		return signalpkg.BuildTerminalErrorSignal(errorClass, nil, 0, 0, nil, nil)
	case cascade.NodeStateFresh:
		return signalpkg.BuildTerminalSuccessSignal(changed, nil, "aggregated_settlement", nil)
	default:
		panic(fmt.Sprintf("parentSettlementSignal: unreachable state %q from Aggregate", state))
	}
}

type PropagationArgs struct {
	NodeRunTree persistence.NodeRunTreeTable
	RunScopes   persistence.RunScopeTable
}

func PropagateFromChildState(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	childRunID shared.UUID,
) ([]CancelAction, []ParentSettlement, error) {
	if args.NodeRunTree == nil {
		return nil, nil, fmt.Errorf("PropagateFromChildState: NodeRunTree is required")
	}

	childRow, err := args.NodeRunTree.GetByID(ctx, tx, childRunID)
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
	if scope == nil || scope.ParentNodeRunID == nil {
		return nil, nil, nil
	}
	return walkUpwards(ctx, args, tx, *scope.ParentNodeRunID)
}

func walkUpwards(
	ctx context.Context, args PropagationArgs, tx persistence.Tx,
	parentNodeRunID shared.UUID,
) ([]CancelAction, []ParentSettlement, error) {
	var actions []CancelAction
	var settlements []ParentSettlement
	current := parentNodeRunID
	for {
		parent, err := args.NodeRunTree.LockTreeForUpdate(ctx, tx, current)
		if err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: lock %s: %w", current, err)
		}
		if parent == nil {
			return actions, settlements, fmt.Errorf("walkUpwards: parent %s not found", current)
		}
		children, err := args.NodeRunTree.ListChildren(ctx, tx, current)
		if err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: list children %s: %w", current, err)
		}
		// @concept: signal
		result := Aggregate(childStatesForAggregate(children), parent.AggregationPolicy)
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
		if isSettled(parent.State) && parent.State == result.ParentState {
			newSig := string(result.ParentSettlingSignalType)
			if err := args.NodeRunTree.UpdateStateAndOutcome(ctx, tx, current, result.ParentState, &newSig, result.ParentChanged); err != nil {
				return actions, settlements, fmt.Errorf("walkUpwards: correct settled parent %s signal: %w", current, err)
			}
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
		if err := args.NodeRunTree.UpdateStateAndOutcome(ctx, tx, current, result.ParentState, newSigArg, result.ParentChanged); err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: update parent %s: %w", current, err)
		}
		if result.Action != AggregateActionNone {
			actions = append(actions, CancelAction{
				ParentNodeRunID: current,
				Kind:            result.Action,
				Children:        children,
			})
		}
		if isSettled(result.ParentState) {
			settlements = append(settlements, ParentSettlement{
				ParentNodeRunID:       current,
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
		// @concept: run-scope
		parentScope, err := args.RunScopes.GetByID(ctx, tx, parent.RunScopeID)
		if err != nil {
			return actions, settlements, fmt.Errorf("walkUpwards: load run scope %s: %w", parent.RunScopeID, err)
		}
		if parentScope == nil || parentScope.ParentNodeRunID == nil {
			return actions, settlements, nil
		}
		current = *parentScope.ParentNodeRunID
	}
}

// @concept: cascade
// @concept: run-scope
type ParentSettlement struct {
	ParentNodeRunID       shared.UUID
	ParentNodeID          shared.UUID
	ParentRunScope        shared.UUID
	FrameID               shared.UUID
	NewState              cascade.NodeState
	NewSettlingSignalType signalpkg.TypePath
	NewChanged            bool
}

type CancelAction struct {
	ParentNodeRunID shared.UUID
	Kind            AggregateAction
	Children        []persistence.NodeRunTreeRow
}

func isSettled(state cascade.NodeState) bool {
	return ChildState{State: state}.IsSettled()
}

func PropagateIfChildAfterTerminal(
	ctx context.Context, args RunArgs,
	runID shared.UUID,
) ([]CancelAction, error) {
	if args.Persist == nil {
		return nil, nil
	}
	rt := args.Persist.NodeRunTree()
	if rt == nil {
		return nil, nil
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return nil, nil
	}
	// @concept: run-scope
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
		parentID = scope.ParentNodeRunID
		return nil
	}); err != nil {
		return nil, err
	}
	if parentID == nil {
		return nil, nil
	}
	var actions []CancelAction
	var settlements []ParentSettlement
	var postCommit postCommitFn
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		outActions, outSettlements, err := PropagateFromChildState(ctx, PropagationArgs{
			NodeRunTree: rt,
			RunScopes:   scopes,
		}, tx, runID)
		actions = outActions
		settlements = outSettlements
		if err != nil {
			return err
		}
		// @concept: cancel-siblings
		cancelPC, err := executeCancelActions(ctx, args, tx, rt, actions)
		if err != nil {
			return fmt.Errorf("PropagateIfChildAfterTerminal: execute cancel actions: %w", err)
		}
		postCommit = chainPostCommit(postCommit, cancelPC)
		// @concept: cascade
		// @concept: run-scope
		for _, s := range settlements {
			if s.FrameID == (shared.UUID{}) {
				if args.Logger != nil {
					args.Logger.Warn("PropagateIfChildAfterTerminal: skip cascade bridge: parent frame_id is zero",
						"parent_run_id", s.ParentNodeRunID.String(),
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
			// @concept: signal
			parentSig := parentSettlementSignal(s.NewState, s.NewSettlingSignalType, s.NewChanged)
			if err := emitSignalInTxOnce(
				ctx, args, tx,
				s.ParentNodeID,
				nodeRow.NodeType,
				s.ParentNodeRunID,
				nodeRow.InstanceID,
				s.FrameID,
				parentSig,
			); err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: cascade parent %s: %w", s.ParentNodeRunID, err)
			}
			if err := upsertDataFromDispatchInputBagIfEmpty(ctx, args, tx, s.ParentNodeRunID, s.ParentNodeID); err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: upsert parent attrs %s: %w", s.ParentNodeRunID, err)
			}
			if err := emitAttributeChangesForRunInTx(ctx, args, tx,
				s.ParentNodeID, nodeRow.NodeType, s.ParentNodeRunID, nodeRow.InstanceID, s.FrameID,
				nil, nil); err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: emit parent attribute changes %s: %w", s.ParentNodeRunID, err)
			}
			// @concept: wait-set
			// @decision: walker-rule-per-sender-node
			if err := drainWaitSetOnSettled(ctx, args, tx, s.FrameID, s.ParentNodeRunID); err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: drain wait-set for parent %s: %w", s.ParentNodeRunID, err)
			}
			pc, err := resolveSettledDelegateCallerClaimsInTx(ctx, args, tx, s, nodeRow)
			if err != nil {
				return fmt.Errorf("PropagateIfChildAfterTerminal: resolve delegate caller claims for %s: %w", s.ParentNodeRunID, err)
			}
			postCommit = chainPostCommit(postCommit, pc)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if postCommit != nil {
		postCommit(ctx)
	}
	return actions, nil
}

// @concept: cancel-siblings
func executeCancelActions(
	ctx context.Context, args RunArgs, tx persistence.Tx, rt persistence.NodeRunTreeTable,
	actions []CancelAction,
) (postCommitFn, error) {
	var post postCommitFn
	for _, action := range actions {
		if action.Kind != AggregateActionCancelNonWinners {
			continue
		}
		for _, child := range action.Children {
			pc, err := cancelInFlightRunTreeSubtree(ctx, args, tx, rt, child.NodeRunID)
			if err != nil {
				return nil, fmt.Errorf("executeCancelActions: cancel %s under parent %s: %w",
					child.NodeRunID, action.ParentNodeRunID, err)
			}
			post = chainPostCommit(post, pc)
		}
	}
	return post, nil
}

// @concept: cancel-siblings
func cancelInFlightRunTreeSubtree(
	ctx context.Context, args RunArgs, tx persistence.Tx, rt persistence.NodeRunTreeTable, runID shared.UUID,
) (postCommitFn, error) {
	current, err := args.Persist.Nodes().GetRunForGate(ctx, tx, runID)
	if err != nil {
		return nil, fmt.Errorf("cancelInFlightRunTreeSubtree: GetRunForGate %s: %w", runID, err)
	}
	if current == nil || cascade.IsTerminal(current.State) {
		return nil, nil
	}
	post, err := cancelInFlightRunTreeChild(ctx, args, tx, current)
	if err != nil {
		return nil, err
	}
	children, err := rt.ListChildren(ctx, tx, runID)
	if err != nil {
		return nil, fmt.Errorf("cancelInFlightRunTreeSubtree: ListChildren %s: %w", runID, err)
	}
	for _, c := range children {
		childPost, err := cancelInFlightRunTreeSubtree(ctx, args, tx, rt, c.NodeRunID)
		if err != nil {
			return nil, err
		}
		post = chainPostCommit(post, childPost)
	}
	return post, nil
}

// @concept: cancel-siblings
func cancelInFlightRunTreeChild(
	ctx context.Context, args RunArgs, tx persistence.Tx, current *persistence.NodeRunForGate,
) (postCommitFn, error) {
	sig := cascade.SettlingSignalSiblingFailed
	if err := args.Persist.Nodes().UpdateState(ctx, current.NodeRunID,
		cascade.NodeStateFailed, cascade.ReasonSiblingCancelled, &sig, tx); err != nil {
		if errors.Is(err, cascade.ErrIllegalTransition) {
			if args.Logger != nil {
				args.Logger.Warn("cancelInFlightRunTreeChild: raced with the sibling's own natural terminal transition; leaving its outcome as-is",
					"node_run_id", current.NodeRunID.String(), "error", err.Error())
			}
			return nil, nil
		}
		return nil, fmt.Errorf("cancelInFlightRunTreeChild: UpdateState %s: %w", current.NodeRunID, err)
	}
	var instanceID shared.UUID
	nodeRow, err := args.Persist.Nodes().Get(ctx, current.NodeID, tx)
	if err != nil {
		return nil, fmt.Errorf("cancelInFlightRunTreeChild: load node %s: %w", current.NodeID, err)
	}
	if nodeRow != nil {
		instanceID = nodeRow.InstanceID
	}
	auditSig := signalpkg.Signal{
		Type:    signalpkg.TypePath(cascade.SettlingSignalSiblingFailed),
		Payload: map[string]any{"error_class": "sibling_failed"},
	}
	var now time.Time
	if args.Clock != nil {
		now = args.Clock.Now()
	}
	if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
		instanceID, current.NodeID, auditSig, now, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("cancelInFlightRunTreeChild: terminal signal audit failed; cancellation stands",
			"node_run_id", current.NodeRunID.String(), "error", err.Error())
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, current.FrameID, current.NodeRunID); err != nil && args.Logger != nil {
		args.Logger.Warn("cancelInFlightRunTreeChild: wait-set drain failed; cancellation stands",
			"node_run_id", current.NodeRunID.String(), "error", err.Error())
	}
	if current.State == cascade.NodeStateHeld {
		failHeldCoHolderRows(ctx, args, tx, current.NodeRunID)
	}
	run := persistence.NodeRunLatest{
		NodeRunID:  current.NodeRunID,
		NodeID:     current.NodeID,
		RunScopeID: current.RunScopeID,
		FrameID:    current.FrameID,
		State:      current.State,
	}
	post := abandonRunClaimsThroughProducers(ctx, args, tx, instanceID, run)
	runID := current.NodeRunID
	propagate := func(pctx context.Context) {
		if _, err := PropagateIfChildAfterTerminal(pctx, args, runID); err != nil && args.Logger != nil {
			args.Logger.Warn("cancelInFlightRunTreeChild: run-tree propagation failed",
				"run_id", runID.String(), "error", err.Error())
		}
	}
	return chainPostCommit(post, propagate), nil
}

func resolveSettledDelegateCallerClaimsInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	s ParentSettlement, parentNode *persistence.NodeRow,
) (postCommitFn, error) {
	tmplSpec, err := loadTemplateSpec(ctx, args, tx, parentNode.InstanceID)
	if err != nil {
		return nil, err
	}
	if tmplSpec == nil {
		return nil, nil
	}
	def := LookupNodeDef(tmplSpec, parentNode.NodeType)
	if def == nil || def.Delegate == "" {
		return nil, nil
	}
	parentRun, err := args.Persist.NodeRunTree().GetByID(ctx, tx, s.ParentNodeRunID)
	if err != nil {
		return nil, err
	}
	if parentRun == nil {
		return nil, nil
	}
	outcome := OutcomeAbandon
	if s.NewState == cascade.NodeStateFresh {
		outcome = OutcomeCommit
	}
	return resolveDelegateCallerClaimsInTx(ctx, args, tx, *parentRun, parentNode.InstanceID, outcome)
}
