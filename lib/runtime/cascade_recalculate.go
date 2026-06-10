// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cascade fallthrough: per-node detection of `pure_cascade` (the
// no-dispatch fresh-roll). When all dependents are fresh and a node
// has no executor, the scheduler's pure-cascade sweep rolls fresh
// state forward without running the node.
//
// @concept: cascade
package runtime

import (
	"context"
	"errors"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// RecalculateArgs is the payload for RecalculateNode.
type RecalculateArgs struct {
	Persist      persistence.Tables
	Queue        persistence.Queue
	Clock        shared.Clock
	Logger       shared.Logger
	SourceNodeID *shared.UUID
	TargetNodeID shared.UUID
}

// RecalculateNode routes a recalculate message to TargetNodeID. Flow:
//  1. Append message_received.
//  2. Load target.
//  3. If fresh: no-op.
//  4. If running or failed: no-op.
//  5. If stale: check all dependencies. If any dep != fresh, no-op (we'll
//     be nudged again when that dep completes). If all fresh AND target
//     has an executor, enqueue dispatch row. If all fresh AND no executor,
//     the scheduler's pure-cascade sweep handles it — no dispatch needed;
//     no-op here.
func RecalculateNode(ctx context.Context, args RecalculateArgs) error {
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	_ = log

	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}

	// Load target BEFORE emitting the audit event so the message_received
	// row carries the owning InstanceID. STORY-event-log-read says an
	// operator filters /v1/events by instance_id and expects to see every
	// event of the instance; an event row without InstanceID is dropped
	// by that filter and silently absent from the unified feed. The prior
	// ordering (emit first, then load) dropped the InstanceID column on
	// every message_received row — fixing here at the read-surface
	// boundary rather than threading instance_id resolution through the
	// payload map keeps the storage shape consistent with every other
	// instance-scoped emit site.
	var target *persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := sb.Nodes().Get(ctx, args.TargetNodeID, tx)
		target = t
		return err
	}); err != nil {
		return err
	}
	if target == nil {
		return nil
	}

	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sb.Events().Append(ctx, persistence.EventAppendInput{
			InstanceID: &target.InstanceID,
			NodeID:     &args.TargetNodeID,
			Kind:       events.KindMessageReceived(),
			Payload: map[string]any{
				"type":           "recalculate",
				"source_node_id": sourceStr,
				"target_node_id": args.TargetNodeID.String(),
			},
		}, tx)
	})
	if target.State != cascade.NodeStateStale {
		// fresh / running / failed — no-op.
		return nil
	}

	// Post-2026-05-14: gating predicate is "wait-set empty in this
	// frame." The cascade-from-commit path inserts wait-set rows; the
	// settled-state drain removes them. If any rows remain, the
	// scheduler's ListReadyForDispatch will pick the row up on a later
	// tick once the drain completes. Post-stage-5 the wait-set keys on
	// receiver_run_id; resolve the target's in-flight run for the frame
	// via the queue. Absent run means we can't gate-check here — bail to
	// the next scheduler tick which seeds the run row via the source.
	if target.FrameID == nil {
		return nil
	}
	var pending int
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		// Under RunScope-first the in-flight resolver keys on
		// (node_id, run_scope_id). Receivers with no in-flight run
		// have nothing for the wait-set walk to gate on; treat that
		// as "no pending blockers" — the next scheduler tick re-runs
		// the gate after the cascade walker affirms a row.
		if target.RunScopeID == nil {
			pending = 0
			return nil
		}
		runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, target.ID, *target.RunScopeID)
		if err != nil {
			return err
		}
		if !ok {
			pending = 0
			return nil
		}
		rows, err := sb.WaitSet().ListForReceiver(ctx, *target.FrameID, runID, tx)
		if err != nil {
			return err
		}
		pending = len(rows)
		return nil
	}); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}

	// All deps fresh. If no executor → pure-cascade sweep handles. If executor → enqueue.
	if target.Executor == "" {
		return nil
	}
	// FrameID is sourced from the target node row — a stale node always
	// belongs to the in-flight frame (blessed-invariant 19). A nil frame_id
	// here means the frame engine hasn't yet advanced the source-node's
	// queued frame; defer to the next scheduler tick.
	if target.FrameID == nil {
		log.Debug("RecalculateNode: skip enqueue: target frame_id is nil",
			"node_id", target.ID.String())
		return nil
	}
	// RequiredStores is intentionally empty here. Per spec §6.2 an empty
	// slice trivially satisfies the supervisor-pool predicate
	// (RequiredStores ⊆ AcceptedStores).
	var runScopeID shared.UUID
	if target.RunScopeID != nil {
		runScopeID = *target.RunScopeID
	}
	// Recovery-aware fields: the in-flight run row on the target
	// (if any) is the predecessor whose output is now stale; surface
	// its id on proto:executor.proto::ExecuteRequest.prior_dispatch_id
	// so executors maintaining per-dispatch session state can recover
	// or hand off the recalculate.
	priorDispatchID := target.InFlightRunID
	if err := args.Queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:                   target.ID,
		ExecutorName:             target.Executor,
		RequiredStores:           []string{},
		EnqueuedAt:               args.Clock.Now(),
		FrameID:                  *target.FrameID,
		RunScopeID:               runScopeID,
		PriorDispatchID:          priorDispatchID,
		PriorDispatchDisposition: "recalculate",
	}); err != nil {
		// Defensive: a closed RunScope means the target's scope has
		// terminated (parent rendezvous has fired). Walker discipline
		// per concept:run-scope: do not enqueue into a closed scope;
		// drop the recalculate silently. Without this skip, a benign
		// race between the source's commit cascade and the target's
		// scope closure would surface as a recalculate error.
		if errors.Is(err, persistence.ErrRunScopeClosed) {
			log.Debug("RecalculateNode: skip enqueue: run scope closed",
				"node_id", target.ID.String(),
				"run_scope_id", runScopeID.String())
			return nil
		}
		return err
	}
	return nil
}
