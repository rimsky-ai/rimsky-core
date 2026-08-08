// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package runtime

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const breakpointQueueCap = 100

const breakpointResumePollInterval = 250 * time.Millisecond

// @concept: breakpoint
type BreakpointInfraError struct {
	Phase string
	Cause error
}

func (e *BreakpointInfraError) Error() string {
	if e.Cause == nil {
		return "breakpoint_infra: " + e.Phase
	}
	return "breakpoint_infra: " + e.Phase + ": " + e.Cause.Error()
}

func (e *BreakpointInfraError) Unwrap() error { return e.Cause }

// @concept: breakpoint
type CheckpointContext struct {
	InstanceID       shared.UUID
	NodeID           shared.UUID
	NodeRunID        shared.UUID
	FrameID          shared.UUID
	Executor         string
	NodeType         string
	Graph            string
	ChildKey         string
	MergedAttributes map[string]any
	Checkpoint       persistence.BreakpointCheckpoint
	TerminalSignal   *signalpkg.Signal
	EffectiveSchema  map[string]any
	NodeRunSnapshot  map[string]any
	HeldClaims       []map[string]any
	OpenWaitSet      []map[string]any
}

func clockNow(args RunArgs) time.Time {
	if args.Clock != nil {
		return args.Clock.Now()
	}
	return time.Now()
}

// @concept: breakpoint
func EvaluateBreakpoints(
	ctx context.Context,
	args RunArgs,
	cc CheckpointContext,
) (map[string]any, error) {
	var bps []persistence.BreakpointRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		bps, err = args.Persist.Breakpoints().ListForInstance(ctx, cc.InstanceID, false, clockNow(args), tx)
		return err
	}); err != nil {
		return cc.MergedAttributes, &BreakpointInfraError{Phase: "list_breakpoints", Cause: err}
	}

	matcherInput := cc.MergedAttributes
	result := cc.MergedAttributes
	for _, bp := range bps {
		if bp.Checkpoint != cc.Checkpoint {
			continue
		}
		if !matcher.Evaluate(matcher.Matcher(bp.Matcher), matcher.Context{
			Executor:     cc.Executor,
			NodeType:     cc.NodeType,
			Graph:        cc.Graph,
			ChildKey:     cc.ChildKey,
			AttributeBag: matcherInput,
		}, args.Logger, 0) {
			continue
		}
		if bp.SignalType != nil {
			if cc.TerminalSignal == nil || !cc.TerminalSignal.Type.HasPrefix(signalpkg.TypePath(*bp.SignalType)) {
				continue
			}
		}

		cc.MergedAttributes = result
		hitID, err := createHitWithinCap(ctx, args, cc, bp)
		if err != nil {
			return result, err
		}

		if bp.Mode == persistence.BreakpointModeNotifyOnly {
			continue
		}

		hit, err := waitForResume(ctx, args, hitID)
		if err != nil {
			return result, err
		}
		if hit != nil && hit.ResumeOverlay != nil {
			merged, _ := shared.DeepMergeJSON(result, hit.ResumeOverlay).(map[string]any)
			if merged != nil {
				result = merged
				matcherInput = result
			}
		}
	}
	return result, nil
}

func createHitWithinCap(ctx context.Context, args RunArgs, cc CheckpointContext, bp persistence.BreakpointRow) (shared.UUID, error) {
	var (
		nodeRunIDPt *shared.UUID
		frameIDPt   *shared.UUID
	)
	if cc.NodeRunID != (shared.UUID{}) {
		id := cc.NodeRunID
		nodeRunIDPt = &id
	}
	if cc.FrameID != (shared.UUID{}) {
		id := cc.FrameID
		frameIDPt = &id
	}
	// @concept: event-log
	nodeIDPt := nodeIDPtr(cc.NodeID)

	warnedUnknownPolicy := false
	for {
		var (
			hitID    shared.UUID
			inserted bool
			dropped  bool
		)
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := args.Persist.Breakpoints().Lock(ctx, bp.ID, tx); err != nil {
				return &BreakpointInfraError{Phase: "overflow_lock", Cause: err}
			}
			unresumed, err := args.Persist.BreakpointHits().UnresumedCount(ctx, bp.ID, tx)
			if err != nil {
				return &BreakpointInfraError{Phase: "overflow_check", Cause: err}
			}
			if unresumed >= breakpointQueueCap {
				switch {
				case bp.OverflowPolicy == persistence.OverflowDropOldest:
					if _, err := args.Persist.BreakpointHits().DropOldest(ctx, bp.ID, breakpointQueueCap-1, tx); err != nil {
						return &BreakpointInfraError{Phase: "drop_oldest", Cause: err}
					}
					if err := args.Persist.Breakpoints().IncrementDropped(ctx, bp.ID, tx); err != nil {
						return &BreakpointInfraError{Phase: "drop_oldest", Cause: err}
					}
				case bp.Mode == persistence.BreakpointModeNotifyOnly:
					// @concept: breakpoint
					if err := args.Persist.Breakpoints().IncrementDropped(ctx, bp.ID, tx); err != nil {
						return &BreakpointInfraError{Phase: "notify_only_overflow", Cause: err}
					}
					dropped = true
					return nil
				default:
					return nil
				}
			}
			hitID, _, err = args.Persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bp.ID,
				InstanceID:   cc.InstanceID,
				NodeRunID:    nodeRunIDPt,
				FrameID:      frameIDPt,
				Checkpoint:   cc.Checkpoint,
				Mode:         bp.Mode,
				Snapshot:     buildSnapshot(cc),
				HitAt:        clockNow(args),
			}, tx)
			if err != nil {
				return &BreakpointInfraError{Phase: "create_hit", Cause: err}
			}
			if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &cc.InstanceID,
				NodeID:     nodeIDPt,
				Kind:       events.KindBreakpointHit(),
				Payload: eventpayload.New(&genv1.BreakpointHitPayload{
					InstanceId:   cc.InstanceID.String(),
					NodeId:       cc.NodeID.String(),
					BreakpointId: bp.ID.String(),
					HitId:        hitID.String(),
					Checkpoint:   string(cc.Checkpoint),
					Mode:         string(bp.Mode),
				}),
			}, tx); err != nil {
				return &BreakpointInfraError{Phase: "create_hit", Cause: err}
			}
			inserted = true
			return nil
		}); err != nil {
			return shared.UUID{}, err
		}
		if inserted {
			return hitID, nil
		}
		if dropped {
			return shared.UUID{}, nil
		}

		switch bp.OverflowPolicy {
		case persistence.OverflowBlockDispatch, persistence.OverflowAutoResumeAfterTTL:
		default:
			if !warnedUnknownPolicy && args.Logger != nil {
				args.Logger.Warn("breakpoint.overflow.unknown_policy",
					"breakpoint_id", bp.ID.String(),
					"overflow_policy", string(bp.OverflowPolicy),
					"action", "defaulting to block until queue drains")
				warnedUnknownPolicy = true
			}
		}
		select {
		case <-ctx.Done():
			return shared.UUID{}, &BreakpointInfraError{Phase: "ctx_cancelled", Cause: ctx.Err()}
		case <-time.After(breakpointResumePollInterval):
		}
	}
}

func waitForResume(ctx context.Context, args RunArgs, hitID shared.UUID) (*persistence.BreakpointHitRow, error) {
	for {
		var hit *persistence.BreakpointHitRow
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			hit, err = args.Persist.BreakpointHits().Get(ctx, hitID, tx)
			return err
		}); err != nil {
			return nil, &BreakpointInfraError{Phase: "wait_resume", Cause: err}
		}
		if hit == nil {
			return nil, nil
		}
		if hit.ResumedAt != nil {
			return hit, nil
		}
		select {
		case <-ctx.Done():
			return nil, &BreakpointInfraError{Phase: "ctx_cancelled", Cause: ctx.Err()}
		case <-time.After(breakpointResumePollInterval):
		}
	}
}

func nodeIDPtr(id shared.UUID) *shared.UUID {
	if id == (shared.UUID{}) {
		return nil
	}
	out := id
	return &out
}

func buildSnapshot(cc CheckpointContext) map[string]any {
	snap := map[string]any{
		"checkpoint": string(cc.Checkpoint),
		"dispatch_context": map[string]any{
			"executor":          cc.Executor,
			"node_type":         cc.NodeType,
			"graph":             cc.Graph,
			"child_key":         cc.ChildKey,
			"merged_attributes": cc.MergedAttributes,
		},
		"node_run":      cc.NodeRunSnapshot,
		"held_claims":   cc.HeldClaims,
		"open_wait_set": cc.OpenWaitSet,
	}
	if len(cc.EffectiveSchema) > 0 {
		snap["effective_schema"] = cc.EffectiveSchema
	}
	if cc.TerminalSignal != nil {
		snap["terminal_signal"] = map[string]any{
			"type":    string(cc.TerminalSignal.Type),
			"payload": cc.TerminalSignal.Payload,
		}
	}
	return snap
}
