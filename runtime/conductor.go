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
	"errors"
	"fmt"
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
		if n.RunScopeID == nil {
			// No in-flight RunScope projected — nothing to transition.
			// Phase B's cascade allocation path is responsible for
			// affirming a row before state-machine writes can land.
			continue
		}
		// Bundle the state-mutation (UpdateState + zombie row retirement +
		// cascade walk) AND the recovery Enqueue in a single tx so a
		// crash between them can't strand the node in state=stale with no
		// in-flight dispatch row. Mirrors the OnError-retry branch's
		// tx-atomic remove+enqueue pair. See @blessed-invariant:
		// "State-machine writes for a single run must be tx-atomic".
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			// Re-read mutable fields (FrameID, InFlightRunID, Executor,
			// RunScopeID) INSIDE this tx so the read-then-write pair is
			// atomic. Using these from the outer batch read (the `stale`
			// slice populated by ListWithStaleHeartbeat) could race with
			// another supervisor's orphan reaper that rotated the
			// in-flight run between the outer batch read and this tx —
			// in which case `n.InFlightRunID` would point at a pre-
			// predecessor instead of the row this tx is about to retire.
			// Per @blessed-invariant: "State-machine writes for a single
			// run must be tx-atomic".
			cur, err := args.Persist.Nodes().Get(ctx, n.ID, tx)
			if err != nil {
				return err
			}
			if cur == nil {
				return nil
			}
			// Use the re-read RunScopeID — a concurrent close + reopen
			// could move the node into a different scope; we want this
			// tx to address whatever scope the node is in NOW.
			if cur.RunScopeID == nil {
				return nil
			}
			curScopeID := *cur.RunScopeID
			// Thread the projected RunScope id so the running →
			// stale transition addresses this specific run row even when
			// fan-out siblings share the same node_id.
			if err := args.Persist.Nodes().UpdateState(ctx, cur.ID, curScopeID,
				cascade.NodeStateStale, cascade.ReasonHeartbeatLost, "", tx); err != nil {
				return err
			}
			// Capture the predecessor dispatch id BEFORE retiring the
			// zombie row. Querying GetInFlightRunForNode inside the
			// same tx returns the row this sweep is about to retire,
			// which is the correct prior_dispatch_id for the recovery
			// enqueue. Falling back to cur.InFlightRunID is safe — the
			// re-read above was tx-atomic with the projection.
			priorDispatchID, _, err := args.Queue.GetInFlightRunForNode(ctx, tx, cur.ID, curScopeID)
			if err != nil {
				return fmt.Errorf("resolve prior dispatch id: %w", err)
			}
			// Retire the zombie row to phase='completed' so the
			// (node_id, run_scope_id) in-flight slot frees up — without
			// this the recovery EnqueueInTx below is blocked by the NOT
			// EXISTS guard on the in-flight uniqueness predicate. Empty
			// expectedClaimedBy: the sweep is by definition retiring a
			// row whose holder is gone; no claimant guard.
			if err := args.Queue.RemoveForNodeInTx(ctx, cur.ID, curScopeID, "", tx); err != nil {
				return fmt.Errorf("retire zombie run: %w", err)
			}
			if cur.FrameID != nil {
				if err := walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx,
					log, cur.ID, cur.InstanceID, *cur.FrameID); err != nil {
					return err
				}
			}
			// Re-enqueue without bumping retry_counter. RequiredStores is
			// left empty — the foundation tick does not have the in-memory
			// template registry threaded through, and an empty
			// RequiredStores trivially satisfies the supervisor-pool
			// predicate (RequiredStores ⊆ AcceptedStores). FrameID is
			// sourced from the node row — heartbeat-lost nodes were
			// running in a frame and that frame_id remains the running
			// frame (per blessed-invariant 19). A nil FrameID skips
			// re-enqueue (the row is stranded only until the next tick
			// re-resolves a frame; the state transition still commits).
			if cur.FrameID == nil {
				log.Warn("tick: skip re-enqueue: node frame_id is nil",
					"node_id", cur.ID.String())
				return nil
			}
			// Recovery-aware fields: the predecessor dispatch_id is the
			// id captured above (or the in-flight projection if the
			// lookup raced with retirement). The new dispatch supersedes
			// it; the executor reads the predecessor id on
			// proto:executor.proto::ExecuteRequest.prior_dispatch_id at
			// dispatch.
			priorPtr := cur.InFlightRunID
			if priorDispatchID != (shared.UUID{}) {
				idCopy := priorDispatchID
				priorPtr = &idCopy
			}
			if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
				NodeID:                   cur.ID,
				ExecutorName:             cur.Executor,
				RequiredStores:           []string{},
				EnqueuedAt:               args.Clock.Now(),
				FrameID:                  *cur.FrameID,
				RunScopeID:               curScopeID,
				PriorDispatchID:          priorPtr,
				PriorDispatchDisposition: "heartbeat_stale",
			}, tx); err != nil {
				// Defensive: closed RunScope means the rendezvous fired
				// while the sweep was preparing the retry. Walker
				// discipline per concept:run-scope: do not enqueue into
				// a closed scope; the state-machine writes above
				// already committed.
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					log.Warn("tick: skip re-enqueue: run scope closed",
						"node_id", cur.ID.String(),
						"run_scope_id", curScopeID.String())
					return nil
				}
				return err
			}
			return nil
		}); err != nil {
			log.Warn("tick: heartbeat_lost state transition failed",
				"node_id", n.ID.String(), "error", err.Error())
			continue
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
		if n.RunScopeID == nil {
			log.Debug("tick: ready-sweep skip: node has no in-flight RunScope",
				"node_id", n.ID.String())
			continue
		}
		if err := args.Queue.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     args.Clock.Now(),
			FrameID:        *n.FrameID,
			RunScopeID:     *n.RunScopeID,
		}); err != nil {
			// Defensive: a closed RunScope means the rendezvous fired
			// while the sweep was preparing the dispatch. Walker
			// discipline per concept:run-scope: skip silently.
			if errors.Is(err, persistence.ErrRunScopeClosed) {
				log.Debug("tick: ready-sweep skip: run scope closed",
					"node_id", n.ID.String(),
					"run_scope_id", n.RunScopeID.String())
				continue
			}
			log.Warn("tick: ready-sweep enqueue failed",
				"node_id", n.ID.String(), "error", err.Error())
		}
	}
	return nil
}
