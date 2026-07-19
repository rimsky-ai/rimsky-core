// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	var maxRetries *int
	if acq.NodeDef != nil && acq.NodeDef.MaxRetries != nil {
		v := *acq.NodeDef.MaxRetries
		maxRetries = &v
	}

	now := args.Clock.Now()
	in := persistence.ParkActiveInput{
		NodeRunID:         acq.NodeRunID,
		ExpectedClaimedBy: args.SupervisorID,
		ParkedAt:          now,
		ResumeAt:          t.ParkResumeAt,
	}

	if err := args.Queue.ParkActiveInTx(ctx, tx, in); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, t.Scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	if maxRetries != nil {
		if err := args.Queue.UpdateDispatchTuningInTx(ctx, tx, acq.NodeRunID, maxRetries); err != nil {
			return nil, fmt.Errorf("applyTerminalPark: %w", err)
		}
	}
	// @concept: signal
	parkSigType := string(parkTerminalSignal(t).Type)
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
		cascade.NodeStateParked, cascade.ReasonHandlerPark, &parkSigType, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, tx,
		acq.NodeID, acq.NodeType, acq.NodeRunID, acq.InstanceID, acq.FrameID,
		nil, nil); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.NodeRunID); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: signal
	parkSig := parkTerminalSignal(t)
	if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
		acq.InstanceID, acq.NodeID, parkSig, now, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: emit signal: %w", err)
	}

	nodeRunID := acq.NodeRunID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
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
		propagateSig := parkSigType
		if _, err := PropagateIfChildAfterTerminal(ctx, args, nodeRunID,
			cascade.NodeStateParked, &propagateSig); err != nil {
			args.Logger.Warn("applyTerminalPark: run-tree propagation failed",
				"run_id", nodeRunID.String(), "error", err.Error())
		}
	}
	return post, nil
}

func shouldSpillBlob(args RunArgs, size int) bool {
	return persistence.ShouldSpillBlob(args.Blob, args.BlobSpillThreshold, size)
}

// @concept: signal
func parkTerminalSignal(t terminalEvent) signalpkg.Signal {
	return signalpkg.Signal{
		Type: "transient/park",
		Payload: map[string]any{
			"resume_at": t.ParkResumeAt,
			"tags":      t.Tags,
		},
	}
}
