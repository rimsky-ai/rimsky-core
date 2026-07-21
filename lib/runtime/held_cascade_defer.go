// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @concept: claim-handle
// @decision: held-as-state-not-phase
func subgraphMembersIncludingType(spec *node.TemplateSpec, nodeType string) map[string]struct{} {
	out := map[string]struct{}{}
	if spec == nil || nodeType == "" {
		return out
	}
	for _, sg := range node.HoldingSubgraphsForTemplate(spec) {
		isMember := false
		for _, m := range sg.Members {
			if m == nodeType {
				isMember = true
				break
			}
		}
		if !isMember {
			continue
		}
		for _, m := range sg.Members {
			out[m] = struct{}{}
		}
	}
	return out
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func subgraphMemberFilter(spec *node.TemplateSpec, senderNodeType string) receiverFilter {
	members := subgraphMembersIncludingType(spec, senderNodeType)
	return func(receiverType string) bool {
		_, ok := members[receiverType]
		return ok
	}
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func subgraphNonMemberFilter(spec *node.TemplateSpec, senderNodeType string) receiverFilter {
	members := subgraphMembersIncludingType(spec, senderNodeType)
	return func(receiverType string) bool {
		_, ok := members[receiverType]
		return !ok
	}
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func runHasActiveHeldClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx, runID foundationshared.UUID,
) (bool, error) {
	active, err := listActiveHeldClaimHandleIDsForRun(ctx, args, tx, runID)
	if err != nil {
		return false, err
	}
	return len(active) > 0, nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func listActiveHeldClaimHandleIDsForRun(
	ctx context.Context, args RunArgs, tx persistence.Tx, runID foundationshared.UUID,
) ([]foundationshared.UUID, error) {
	if args.Persist == nil || args.Persist.ClaimHandles() == nil {
		return nil, nil
	}
	seen := map[foundationshared.UUID]struct{}{}
	var out []foundationshared.UUID

	handles, err := args.Persist.ClaimHandles().ListByNodeRun(ctx, runID, tx)
	if err != nil {
		return nil, fmt.Errorf("listActiveHeldClaimHandleIDsForRun: ListByNodeRun: %w", err)
	}
	for _, h := range handles {
		if h.State != spec.ClaimHandleStateActive {
			continue
		}
		if _, ok := seen[h.ID]; ok {
			continue
		}
		seen[h.ID] = struct{}{}
		out = append(out, h.ID)
	}

	if args.Persist.ClaimHolders() != nil {
		holders, err := args.Persist.ClaimHolders().ListByHolderRun(ctx, runID, tx)
		if err != nil {
			return nil, fmt.Errorf("listActiveHeldClaimHandleIDsForRun: ListByHolderRun: %w", err)
		}
		for _, h := range holders {
			if h.State != persistence.ClaimHolderStateActive {
				continue
			}
			ch, err := args.Persist.ClaimHandles().Get(ctx, h.ClaimHandleID, tx)
			if err != nil {
				return nil, fmt.Errorf("listActiveHeldClaimHandleIDsForRun: ClaimHandles.Get: %w", err)
			}
			if ch == nil || ch.State != spec.ClaimHandleStateActive {
				continue
			}
			if _, ok := seen[ch.ID]; ok {
				continue
			}
			seen[ch.ID] = struct{}{}
			out = append(out, ch.ID)
		}
	}
	return out, nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
type holderPortfolioStatus struct {
	FullyResolved bool
	Poisoned      bool
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func evaluateHolderPortfolio(
	ctx context.Context, args RunArgs, tx persistence.Tx, runID foundationshared.UUID,
) (holderPortfolioStatus, error) {
	out := holderPortfolioStatus{FullyResolved: true}
	if args.Persist == nil || args.Persist.ClaimHandles() == nil {
		return out, nil
	}

	handles, err := args.Persist.ClaimHandles().ListByNodeRun(ctx, runID, tx)
	if err != nil {
		return out, fmt.Errorf("evaluateHolderPortfolio: ListByNodeRun: %w", err)
	}
	for _, h := range handles {
		switch h.State {
		case spec.ClaimHandleStateActive:
			out.FullyResolved = false
		case spec.ClaimHandleStateAbandoned:
			out.Poisoned = true
		}
	}

	if args.Persist.ClaimHolders() != nil {
		holders, err := args.Persist.ClaimHolders().ListByHolderRun(ctx, runID, tx)
		if err != nil {
			return out, fmt.Errorf("evaluateHolderPortfolio: ListByHolderRun: %w", err)
		}
		for _, h := range holders {
			ch, err := args.Persist.ClaimHandles().Get(ctx, h.ClaimHandleID, tx)
			if err != nil {
				return out, fmt.Errorf("evaluateHolderPortfolio: ClaimHandles.Get: %w", err)
			}
			if ch == nil {
				continue
			}
			switch ch.State {
			case spec.ClaimHandleStateActive:
				out.FullyResolved = false
			case spec.ClaimHandleStateAbandoned:
				out.Poisoned = true
			}
		}
	}

	return out, nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
// @decision: terminal-error-abandoned-as-error-class
func emitDeferredHeldCascade(
	ctx context.Context, args RunArgs, tx persistence.Tx, td TerminalDecision,
) (postCommitFn, error) {
	if td.Source != HeldTerminal {
		return nil, nil
	}
	if args.Persist == nil || args.Persist.ClaimHolders() == nil {
		return nil, nil
	}
	holders, err := args.Persist.ClaimHolders().ListByClaimHandleID(ctx, td.ClaimHandleID, tx)
	if err != nil {
		return nil, fmt.Errorf("emitDeferredHeldCascade: ListByClaimHandleID: %w", err)
	}
	var post postCommitFn
	visited := map[foundationshared.UUID]struct{}{}
	for _, h := range holders {
		visited[h.HolderNodeRunID] = struct{}{}
		pc, err := transitionHolderIfFullyResolved(ctx, args, tx, h.HolderNodeRunID)
		if err != nil {
			return nil, fmt.Errorf("emitDeferredHeldCascade: holder %s: %w", h.HolderNodeRunID, err)
		}
		post = chainPostCommit(post, pc)
	}
	if td.LineageHint.NodeRunID != (foundationshared.UUID{}) {
		if _, seen := visited[td.LineageHint.NodeRunID]; !seen {
			pc, err := transitionHolderIfFullyResolved(ctx, args, tx, td.LineageHint.NodeRunID)
			if err != nil {
				return nil, fmt.Errorf("emitDeferredHeldCascade: acquirer %s: %w", td.LineageHint.NodeRunID, err)
			}
			post = chainPostCommit(post, pc)
		}
	}
	return post, nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func transitionThisHolderIfFullyResolved(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition,
) (postCommitFn, error) {
	return transitionHolderIfFullyResolved(ctx, args, tx, acq.NodeRunID)
}

// @decision: held-as-state-not-phase
func holderSettlementAttributes(
	ctx context.Context, args RunArgs, tx persistence.Tx, holderNodeRunID foundationshared.UUID,
) (map[string]any, error) {
	if args.Persist == nil || args.Persist.NodeAttributes() == nil {
		return nil, nil
	}
	row, err := args.Persist.NodeAttributes().GetByRun(ctx, holderNodeRunID, tx)
	if err != nil {
		return nil, fmt.Errorf("holderSettlementAttributes: GetByRun %s: %w", holderNodeRunID, err)
	}
	if row == nil || len(row.Data) == 0 {
		return nil, nil
	}
	return row.Data, nil
}

// @concept: auto-terminal
// @concept: run-scope
func holderIsAggregatingParent(
	ctx context.Context, args RunArgs, tx persistence.Tx, holderNodeRunID foundationshared.UUID,
) (bool, error) {
	if args.Persist == nil || args.Persist.NodeRunTree() == nil {
		return false, nil
	}
	children, err := args.Persist.NodeRunTree().ListChildren(ctx, tx, holderNodeRunID)
	if err != nil {
		return false, fmt.Errorf("holderIsAggregatingParent: ListChildren: %w", err)
	}
	return len(children) > 0, nil
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func transitionHolderIfFullyResolved(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	holderNodeRunID foundationshared.UUID,
) (postCommitFn, error) {
	if holderNodeRunID == (foundationshared.UUID{}) {
		return nil, nil
	}
	holderRun, err := args.Persist.Nodes().GetRunForGate(ctx, tx, holderNodeRunID)
	if err != nil {
		return nil, fmt.Errorf("GetRunForGate: %w", err)
	}
	if holderRun == nil || holderRun.State != cascade.NodeStateHeld {
		return nil, nil
	}
	aggregating, err := holderIsAggregatingParent(ctx, args, tx, holderNodeRunID)
	if err != nil {
		return nil, err
	}
	if aggregating {
		return nil, nil
	}
	status, err := evaluateHolderPortfolio(ctx, args, tx, holderNodeRunID)
	if err != nil {
		return nil, err
	}
	if !status.FullyResolved {
		return nil, nil
	}
	var (
		newState cascade.NodeState
		reason   cascade.TransitionReason
		sigType  string
		sig      signalpkg.Signal
	)
	holderOutcome := OutcomeCommit
	if status.Poisoned {
		holderOutcome = OutcomeAbandon
	}
	changeSummary := "auto-terminal/" + outcomeVerbName(holderOutcome)
	if status.Poisoned {
		newState = cascade.NodeStateFailed
		reason = cascade.ReasonAutoTerminalAbandon
		sigType = "terminal/error/abandoned"
		sig = signalpkg.BuildTerminalErrorSignal("abandoned", nil, 0, 0, nil, nil)
	} else {
		newState = cascade.NodeStateFresh
		reason = cascade.ReasonAutoTerminalCommit
		sigType = "terminal/success"
		delta, err := holderSettlementAttributes(ctx, args, tx, holderNodeRunID)
		if err != nil {
			return nil, err
		}
		sig = signalpkg.BuildTerminalSuccessSignal(false, delta, changeSummary, nil)
	}
	if err := args.Persist.Nodes().UpdateState(
		ctx, holderNodeRunID, newState, reason, &sigType, tx,
	); err != nil {
		if errors.Is(err, cascade.ErrIllegalTransition) {
			if args.Logger != nil {
				args.Logger.Warn("transitionHolderIfFullyResolved: raced with another terminal writer; leaving the settled verdict as-is",
					"node_run_id", holderNodeRunID.String(), "error", err.Error())
			}
			return nil, nil
		}
		return nil, fmt.Errorf("UpdateState: %w", err)
	}
	if err := recordRunTreeChanged(ctx, args, tx, holderNodeRunID, newState, &sigType, false); err != nil {
		return nil, fmt.Errorf("transitionHolderIfFullyResolved: %w", err)
	}
	holderNode, err := args.Persist.Nodes().Get(ctx, holderRun.NodeID, tx)
	if err != nil || holderNode == nil {
		return nil, err
	}
	tmplSpec, terr := loadTemplateSpec(ctx, args, tx, holderNode.InstanceID)
	if terr != nil {
		return nil, fmt.Errorf("load template: %w", terr)
	}
	filter := subgraphNonMemberFilter(tmplSpec, holderNode.NodeType)
	visited := map[foundationshared.UUID]struct{}{}
	if err := emitSignalInTxWithFilter(ctx, args, tx,
		holderRun.NodeID, holderNode.NodeType, holderRun.NodeRunID,
		holderNode.InstanceID, holderRun.FrameID, sig, visited, filter); err != nil {
		return nil, fmt.Errorf("emit signal: %w", err)
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, holderRun.FrameID, holderRun.NodeRunID); err != nil {
		return nil, err
	}
	runID := holderRun.NodeRunID
	post := func(ctx context.Context) {
		if _, err := PropagateIfChildAfterTerminal(ctx, args, runID); err != nil {
			if args.Logger != nil {
				args.Logger.Warn("transitionHolderIfFullyResolved: run-tree propagation failed",
					"run_id", runID.String(), "error", err.Error())
			}
		}
	}
	return post, nil
}
