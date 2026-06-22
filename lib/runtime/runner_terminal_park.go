// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: parked-state
func parkReasonStorageForm(r genv1.ParkReason) string {
	s := strings.TrimPrefix(r.String(), "PARK_REASON_")
	return strings.ToLower(s)
}

// @concept: parked-state
func parkReasonFromStorageForm(s string) genv1.ParkReason {
	upper := "PARK_REASON_" + strings.ToUpper(s)
	if v, ok := genv1.ParkReason_value[upper]; ok {
		return genv1.ParkReason(v)
	}
	return genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK
}

func applyTerminalPark(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {

	var maxParkSec *int
	if acq.NodeDef != nil && acq.NodeDef.MaxParkDuration != "" {
		if d, err := time.ParseDuration(acq.NodeDef.MaxParkDuration); err == nil {
			s := int(d.Seconds())
			if s > 0 {
				maxParkSec = &s
			}
		}
	}
	var maxRetries *int
	if acq.NodeDef != nil && acq.NodeDef.MaxRetries != nil {
		v := *acq.NodeDef.MaxRetries
		maxRetries = &v
	}

	now := args.Clock.Now()
	in := persistence.ParkActiveInput{
		DispatchID:        acq.DispatchID,
		ExpectedClaimedBy: args.SupervisorID,
		ParkedAt:          now,
		ResumeAt:          t.ParkResumeAt,
		Reason:            parkReasonStorageForm(t.ParkReason),
		ReasonNote:        t.ParkReasonNote,
		ReasonLabel:       t.ParkReasonLabel,
	}

	if err := args.Queue.ParkActiveInTx(ctx, tx, in); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: attribute
	if len(t.AttributesDel) > 0 {
		merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalPark: upsert attributes_delta: %w", err)
		}
	}
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, t.Scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	if maxParkSec != nil || maxRetries != nil {
		if err := args.Queue.UpdateDispatchTuningInTx(ctx, tx, acq.DispatchID, maxParkSec, maxRetries); err != nil {
			return nil, fmt.Errorf("applyTerminalPark: %w", err)
		}
	}
	// @concept: signal
	parkSigType := string(parkTerminalSignal(t).Type)
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateParked, cascade.ReasonHandlerPark, &parkSigType, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @concept: signal
	parkSig := parkTerminalSignal(t)
	if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
		acq.InstanceID, acq.NodeID, parkSig, now, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: emit signal: %w", err)
	}

	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
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
			ParentRunID:        scope.ParentRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		propagateSig := parkSigType
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateParked, &propagateSig); err != nil {
			args.Logger.Warn("applyTerminalPark: run-tree propagation failed",
				"run_id", dispatchID.String(), "error", err.Error())
		}
	}
	return post, nil
}

func shouldSpillBlob(args RunArgs, size int) bool {
	return persistence.ShouldSpillBlob(args.Blob, args.BlobSpillThreshold, size)
}

// @concept: signal
func parkTerminalSignal(t terminalEvent) signalpkg.Signal {
	payload := map[string]any{
		"parked_reason_label": t.ParkReasonLabel,
		"parked_reason_note":  t.ParkReasonNote,
		"tags":                t.Tags,
	}
	if t.ParkReason == genv1.ParkReason_PARK_REASON_SNOOZE {
		payload["resume_at"] = t.ParkResumeAt
		return signalpkg.Signal{
			Type:    "terminal/park/snooze",
			Payload: payload,
		}
	}
	if !t.ParkResumeAt.IsZero() {
		payload["resume_at"] = t.ParkResumeAt
	}
	return signalpkg.Signal{
		Type:    "terminal/park/await_callback",
		Payload: payload,
	}
}
