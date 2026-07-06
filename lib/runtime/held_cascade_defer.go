// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
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
		visited[h.HolderRunID] = struct{}{}
		pc, err := transitionHolderIfFullyResolved(ctx, args, tx, h.HolderRunID, td)
		if err != nil {
			return nil, fmt.Errorf("emitDeferredHeldCascade: holder %s: %w", h.HolderRunID, err)
		}
		post = chainPostCommit(post, pc)
	}
	if td.LineageHint.RunID != (foundationshared.UUID{}) {
		if _, seen := visited[td.LineageHint.RunID]; !seen {
			pc, err := transitionHolderIfFullyResolved(ctx, args, tx, td.LineageHint.RunID, td)
			if err != nil {
				return nil, fmt.Errorf("emitDeferredHeldCascade: acquirer %s: %w", td.LineageHint.RunID, err)
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
	status, err := evaluateHolderPortfolio(ctx, args, tx, acq.DispatchID)
	if err != nil {
		return nil, err
	}
	if !status.FullyResolved {
		return nil, nil
	}
	outcome := OutcomeCommit
	if status.Poisoned {
		outcome = OutcomeAbandon
	}
	td := TerminalDecision{
		ClaimHandleID: foundationshared.UUID{},
		Outcome:       outcome,
		LineageHint:   ClaimLineageHint{RunID: acq.DispatchID},
	}
	return transitionHolderIfFullyResolved(ctx, args, tx, acq.DispatchID, td)
}

// @concept: claim-handle
// @decision: held-as-state-not-phase
func transitionHolderIfFullyResolved(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	holderRunID foundationshared.UUID, td TerminalDecision,
) (postCommitFn, error) {
	if holderRunID == (foundationshared.UUID{}) {
		return nil, nil
	}
	holderRun, err := args.Persist.Nodes().GetRunForGate(ctx, tx, holderRunID)
	if err != nil {
		return nil, fmt.Errorf("GetRunForGate: %w", err)
	}
	if holderRun == nil || holderRun.State != cascade.NodeStateHeld {
		return nil, nil
	}
	status, err := evaluateHolderPortfolio(ctx, args, tx, holderRunID)
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
	changeSummary := "auto-terminal/" + outcomeVerbName(td.Outcome)
	if status.Poisoned {
		newState = cascade.NodeStateFailed
		reason = cascade.ReasonAutoTerminalAbandon
		sigType = "terminal/error/abandoned"
		sig = signalpkg.BuildTerminalErrorSignal("abandoned", nil, 0, 0, nil, nil)
	} else {
		newState = cascade.NodeStateFresh
		reason = cascade.ReasonAutoTerminalCommit
		sigType = "terminal/success"
		sig = signalpkg.BuildTerminalSuccessSignal(false, nil, changeSummary, nil)
	}
	if err := args.Persist.Nodes().UpdateState(
		ctx, holderRunID, newState, reason, &sigType, tx,
	); err != nil {
		return nil, fmt.Errorf("UpdateState: %w", err)
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
		holderRun.NodeID, holderNode.NodeType, holderRun.RunID,
		holderNode.InstanceID, holderRun.FrameID, sig, visited, filter); err != nil {
		return nil, fmt.Errorf("emit signal: %w", err)
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, holderRun.FrameID, holderRun.RunID); err != nil {
		return nil, err
	}
	runID := holderRun.RunID
	logSigType := sigType
	post := func(ctx context.Context) {
		if _, err := PropagateIfChildAfterTerminal(ctx, args, runID, newState, &logSigType); err != nil {
			if args.Logger != nil {
				args.Logger.Warn("transitionHolderIfFullyResolved: run-tree propagation failed",
					"run_id", runID.String(), "error", err.Error())
			}
		}
	}
	return post, nil
}
