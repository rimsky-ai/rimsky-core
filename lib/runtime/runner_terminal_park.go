// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
)

func applyTerminalPark(
	ctx context.Context, args RunArgs, acq *acquisition,
	t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {

	now := args.Clock.Now()
	in := persistence.ParkActiveInput{
		NodeRunID:         acq.NodeRunID,
		ExpectedClaimedBy: args.SupervisorID,
		ParkedAt:          now,
		ResumeAt:          t.ParkResumeAt,
	}

	if err := args.Queue.ParkActive(ctx, in, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: executor
	// @decision: no-resume-context
	if err := applyTerminalScratchInTx(ctx, args, acq, t.Scratch, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: signal
	parkSigType := string(parkTerminalSignal(args, t).Type)
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
		cascade.NodeStateParked, cascade.ReasonHandlerPark, &parkSigType, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: signal
	parkSig := parkTerminalSignal(args, t)
	if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
		acq.InstanceID, acq.NodeID, parkSig, now, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: emit signal: %w", err)
	}

	nodeRunID := acq.NodeRunID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq, nil)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			NodeRunID:          nodeRunID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateParked),
			SettlingSignalType: parkSigType,
			TerminalKind:       "park",
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentNodeRunID:    scope.ParentNodeRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		if _, err := PropagateIfChildAfterTerminal(ctx, args, nodeRunID); err != nil {
			args.Logger.Warn("applyTerminalPark: run-tree propagation failed",
				"run_id", nodeRunID.String(), "error", err.Error())
		}
	}
	return post, nil
}

// @concept: signal
// @concept: executor
func parkTerminalSignal(args RunArgs, t terminalEvent) signalpkg.Signal {
	scratchSize := len(t.Scratch)
	payload := signalpkg.PayloadMap(signalpkg.TransientParkPayload{
		ResumeAt: t.ParkResumeAt,
		Tags:     t.Tags,
	})
	payload["scratch_size"] = scratchSize
	payload["scratch_spilled"] = shouldSpillBlob(args, scratchSize)
	return signalpkg.Signal{
		Type:    "transient/park",
		Payload: payload,
	}
}
