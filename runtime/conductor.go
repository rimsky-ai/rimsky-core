// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Foundation conductor — tick-time foundation sweeps.
//
// Per the foundation contract, the conductor is the integration-layer
// orchestrator that gates each tick under the advisory lock and runs
// the foundation sweeps:
//
//   - Stale-heartbeat sweep: running nodes whose last_heartbeat is older
//     than the cutoff are forced running→stale, supervisor assignment is
//     cleared, a heartbeat_lost event is appended, and the node is
//     re-enqueued (no retry bump — infra event, not application error).
//
//   - Dispatch-claim orphan sweep: dispatch rows whose `last_heartbeat_at`
//     is older than the cutoff are released claimant-guarded so a fresh
//     supervisor can pick them up.
//
//   - Lock-holder orphan reap: see orphan_reaper.go (called by callers
//     out of band; foundation provides the primitive).
//
//   - Ready sweep: executor-backed stale nodes whose deps are all fresh
//     get enqueued for the next claim cycle.
//
// The foundation conductor does not run the graph-layer sweeps
// (cron schedules, pure-cascade transitions, frame-engine ticks).
// Graph-layer ticks call into these foundation primitives separately.
//
// @blessed-invariant 7: Advisory lock on scheduler tick. Postgres uses
// `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`; SQLite uses
// `sync.Mutex`. Skips the tick when another replica holds it. The
// caller is responsible for invoking AdvisoryLocker.TrySchedulerTick
// before calling these sweeps; the foundation primitives do not gate
// themselves.
package runtime

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// ConductorArgs bundles the dependencies for the foundation sweeps.
//
// HeartbeatTimeout is the cutoff used by SweepStaleHeartbeats.
// OrphanedClaimTimeout is the shared 5×heartbeat_interval cutoff used
// by `SweepOrphanedNodeRuns` and `SweepOrphanedClaimHandles` (per
// `@blessed-invariant 6`). The constant name retains the legacy
// "Claim" label even though the two reaped row kinds are now named
// rimsky_node_runs and rimsky_claim_handles.
type ConductorArgs struct {
	Persist              persistence.Tables
	Queue                persistence.Queue
	Clock                shared.Clock
	Logger               shared.Logger
	HeartbeatTimeout     time.Duration
	OrphanedClaimTimeout time.Duration
}

// SweepStaleHeartbeats finds running nodes whose last_heartbeat is
// older than now - HeartbeatTimeout, transitions them to stale, and
// re-enqueues them for the next claim cycle.
func SweepStaleHeartbeats(ctx context.Context, args ConductorArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	cutoff := args.Clock.Now().Add(-args.HeartbeatTimeout)
	var stale []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.Nodes().ListWithStaleHeartbeat(ctx, cutoff, tx)
		stale = rows
		return err
	}); err != nil {
		return err
	}
	for _, n := range stale {
		nodeID := n.ID
		instanceID := n.InstanceID
		payload := map[string]any{
			"supervisor_id":     n.AssignedSupervisorID,
			"last_heartbeat_at": n.LastHeartbeatAt,
		}
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &nodeID, InstanceID: &instanceID,
				Kind:    "heartbeat_lost",
				Payload: payload,
			}, tx)
		}); err != nil {
			log.Warn("tick: append heartbeat_lost failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
		// running → stale (also clears assigned_supervisor_id + heartbeat
		// as part of the state transition; no separate clear call needed).
		//
		// Pessimistic-invalidate per spec Piece 1: the running → stale
		// transition IS this sender's invalidation in this frame. Gate
		// downstream subscribers so they don't dispatch with stale
		// upstream data while the re-enqueued sender is re-running.
		//
		//	@concept: cascade
		//	@concept: wait-set
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := args.Persist.Nodes().UpdateState(ctx, n.ID,
				cascade.NodeStateStale, cascade.ReasonHeartbeatLost, "", tx); err != nil {
				return err
			}
			if n.FrameID == nil {
				return nil
			}
			return walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx,
				log, n.ID, n.InstanceID, *n.FrameID)
		}); err != nil {
			log.Warn("tick: heartbeat_lost state transition failed",
				"node_id", n.ID.String(), "error", err.Error())
			continue
		}
		// Re-enqueue without bumping retry_counter. RequiredStores is left
		// empty — the foundation tick does not have the in-memory template
		// registry threaded through, and an empty RequiredStores trivially
		// satisfies the supervisor-pool predicate
		// (RequiredStores ⊆ AcceptedStores). FrameID is sourced from the
		// node row — heartbeat-lost nodes were running in a frame and
		// that frame_id remains the running frame (per
		// blessed-invariant 19).
		if n.FrameID == nil {
			log.Warn("tick: skip re-enqueue: node frame_id is nil",
				"node_id", n.ID.String())
			continue
		}
		if err := args.Queue.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     args.Clock.Now(),
			FrameID:        *n.FrameID,
		}); err != nil {
			log.Warn("tick: re-enqueue after heartbeat_lost failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
	}
	return nil
}

// SweepOrphanedNodeRuns releases dispatch rows whose last_heartbeat_at is
// older than now - OrphanedClaimTimeout, claimant-guarded so a fresh
// supervisor can pick them up.
//
// Per spec §7.4: the predicate column is `rimsky_node_runs.last_heartbeat_at`.
// Distinct from the §7.5 lock-holder orphan reaper in orphan_reaper.go
// which keys on `rimsky_claim_handles.expires_at`.
func SweepOrphanedNodeRuns(ctx context.Context, args ConductorArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	cutoff := args.Clock.Now().Add(-args.OrphanedClaimTimeout)
	orphans, err := args.Queue.ListOrphanedClaims(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, o := range orphans {
		nodeID := o.NodeID
		prior := ""
		if o.ClaimedBy != nil {
			prior = *o.ClaimedBy
		}
		// Claimant-guarded release: passing prior ensures a fresh supervisor
		// that re-claimed the row between ListOrphanedClaims and this UPDATE
		// keeps its live claim intact.
		if err := args.Queue.ReleaseClaim(ctx, o.ID, prior); err != nil {
			log.Warn("tick: release orphaned claim failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
			continue
		}
		var hb time.Time
		if o.LastHeartbeatAt != nil {
			hb = *o.LastHeartbeatAt
		}
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &nodeID,
				Kind:   "orphaned_claim_released",
				Payload: map[string]any{
					"dispatch_id":       o.ID.String(),
					"prior_claimed_by":  prior,
					"last_heartbeat_at": hb,
				},
			}, tx)
		}); err != nil {
			log.Warn("tick: append orphaned_claim_released failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
		}
	}
	return nil
}

// SweepReady enqueues executor-backed stale nodes whose deps are all
// fresh for the next claim cycle.
func SweepReady(ctx context.Context, args ConductorArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	var ready []persistence.NodeRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.Nodes().ListReadyForDispatch(ctx, tx)
		ready = rows
		return err
	}); err != nil {
		return err
	}
	for _, n := range ready {
		// RequiredStores is left empty; mirrors SweepStaleHeartbeats.
		// FrameID is sourced from the node row — every stale node is
		// part of the in-flight frame (blessed-invariant 19). A nil
		// frame_id here means the frame engine has not yet advanced this
		// node's queued frame; we skip and re-evaluate next tick.
		if n.FrameID == nil {
			log.Debug("tick: ready-sweep skip: node frame_id is nil",
				"node_id", n.ID.String())
			continue
		}
		if err := args.Queue.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     args.Clock.Now(),
			FrameID:        *n.FrameID,
		}); err != nil {
			log.Warn("tick: ready-sweep enqueue failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
	}
	return nil
}
