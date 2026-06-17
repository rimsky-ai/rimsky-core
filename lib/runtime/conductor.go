// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Foundation conductor — tick-time foundation sweeps.
//
// Per the foundation contract, the conductor is the integration-layer
// orchestrator that gates each tick under the advisory lock and runs
// the foundation sweeps:
//
//   - Dispatch-claim orphan sweep: async-mode dispatch rows whose
//     `last_progress_at` is older than the cutoff are released
//     claimant-guarded so a fresh supervisor can pick them up.
//   - Lock-holder orphan reap: see orphan_reaper.go (called by callers
//     out of band; foundation provides the primitive).
//   - Ready sweep: executor-backed stale nodes whose deps are all fresh
//     get enqueued for the next claim cycle.
//
// Per TD-three-dispatch-deadlines + TD-persist-async-callback-registry,
// orphan detection no longer keys on a per-dispatch heartbeat
// timestamp. Sync-mode dispatches surface failure through the
// supervisor's gRPC connection state (in-band); async-mode dispatches
// surface failure through the `max_quiet_period` and `max_runtime`
// deadlines walked by lib/graph/scheduler/scheduler.go's
// SweepExecutorDeadlines. This file's orphan sweep is the persistence-
// layer side of the async deadline path: it picks the rows
// scheduler-side flagged as quiet-too-long and releases their claims.
//
// The foundation conductor does not run the graph-layer sweeps
// (cron schedules, pure-cascade transitions, frame-engine ticks).
// Graph-layer ticks call into these foundation primitives separately.
//
// @blessed-invariant 7: Advisory lock on scheduler tick. Postgres uses
// `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`; SQLite uses
// `sync.Mutex`. Skips the tick when another replica holds it, and a
// lock-attempt error is treated as lock-held (skip the pass, never run
// unlocked). The caller is responsible for invoking
// AdvisoryLocker.TrySchedulerTick before calling these sweeps; the
// foundation primitives do not gate themselves.
package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ConductorArgs bundles the dependencies for the foundation sweeps.
//
// MaxQuietPeriodDefault and MaxRuntimeDefault are the deployment-level
// fallback values for the two operator-opt-in dispatch deadlines. They
// participate in the resolution chain (per-node template value folded
// over deployment default folded over built-in 0=disabled) which
// runs at AwaitAsyncCallback-registration time; the resolved per-row
// values land on col:rimsky_node_runs.effective_max_quiet_period_seconds
// and col:rimsky_node_runs.effective_max_runtime_seconds, which is what
// SweepExecutorDeadlines reads.
//
// @concept: dispatch-deadlines
type ConductorArgs struct {
	Persist               persistence.Tables
	Queue                 persistence.Queue
	Clock                 shared.Clock
	Logger                shared.Logger
	MaxQuietPeriodDefault time.Duration
	MaxRuntimeDefault     time.Duration
}

// SweepExecutorDeadlines releases async-mode dispatch rows whose
// per-row effective max_quiet_period or max_runtime have elapsed,
// claimant-guarded so a fresh supervisor can pick them up. The
// per-row effective values are denormalized at AwaitAsyncCallback
// registration time (see runner_dispatch.go::registerAsyncIfSet);
// this sweep iterates in-flight async rows and decides release
// per-row based on the two deadlines + the appropriate timestamp
// (last_progress_at for quiet, claimed_at for runtime). The
// persistence-layer side of the executor-quiet / max_runtime path;
// sync-mode dispatches surface failure via the supervisor's gRPC
// connection state and never reach this sweep.
//
// @concept: dispatch-deadlines
// @decision: dispatch-deadlines
//
// @agent-contract:
//   - What: enforces per-row max_quiet_period and max_runtime by
//     releasing claims with the matching error_class event
//     (executor_quiet / max_runtime_exceeded). Per-row values were
//     resolved per-node at AwaitAsyncCallback registration; this
//     sweep is purely the time-comparison + release step.
//   - Idempotent: release of an already-released row is a no-op
//     thanks to the claimant-guarded UPDATE.
func SweepExecutorDeadlines(ctx context.Context, args ConductorArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	now := args.Clock.Now()
	// @constraint: ListOrphanedClaims returns the full set of claimed
	// async-mode dispatches; the per-node × per-deadline matrix is
	// evaluated in Go below using the per-row denormalized effective_*
	// columns.
	candidates, err := args.Queue.ListOrphanedClaims(ctx)
	if err != nil {
		return err
	}
	for _, o := range candidates {
		nodeID := o.NodeID
		prior := ""
		if o.ClaimedBy != nil {
			prior = *o.ClaimedBy
		}
		errorClass, lastProgress, releaseReason := decideExecutorDeadlineRelease(o, now)
		if errorClass == "" {
			continue
		}
		// @constraint: claimant-guarded release — passing prior ensures a fresh supervisor that re-claimed the row between ListOrphanedClaims and this UPDATE keeps its live claim intact.
		if err := args.Queue.ReleaseClaim(ctx, o.ID, prior); err != nil {
			log.Warn("tick: release orphaned claim failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
			continue
		}
		// @constraint: resolve the owning instance_id by fetching the node row so the orphaned_claim_released event surfaces on the instance-scoped /v1/events feed (operator filters by instance_id); without InstanceID set, the row is dropped by the events read filter and the orphan-recovery audit trail is silently invisible. A lookup failure here is non-fatal — we still append the event with NodeID alone rather than skipping it, so the global feed retains the orphan record.
		// @story: event-log-read
		var instancePtr *shared.UUID
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			n, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
			if err != nil || n == nil {
				return err
			}
			instID := n.InstanceID
			instancePtr = &instID
			return nil
		}); err != nil {
			log.Warn("tick: orphan node lookup failed; emit without instance_id",
				"node_id", nodeID.String(), "error", err.Error())
		}
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: instancePtr,
				NodeID:     &nodeID,
				Kind:       events.KindOrphanedClaimReleased(),
				Payload: map[string]any{
					"dispatch_id":      o.ID.String(),
					"prior_claimed_by": prior,
					"last_progress_at": lastProgress,
					"error_class":      errorClass,
					"reason":           releaseReason,
				},
			}, tx)
		}); err != nil {
			log.Warn("tick: append orphaned_claim_released failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
		}
	}
	return nil
}

// decideExecutorDeadlineRelease evaluates the per-row effective
// max_quiet_period and max_runtime against the appropriate timestamps.
// Returns (errorClass, lastProgress, reason); errorClass is "" when no
// deadline has elapsed. max_runtime wins over max_quiet_period when both
// have elapsed — max_runtime is the absolute upper bound and is the
// more accurate description of the failure.
func decideExecutorDeadlineRelease(o persistence.DispatchRow, now time.Time) (string, time.Time, string) {
	var lastProgress time.Time
	if o.LastProgressAt != nil {
		lastProgress = *o.LastProgressAt
	}
	if o.EffectiveMaxRuntimeSeconds != nil && *o.EffectiveMaxRuntimeSeconds > 0 && o.ClaimedAt != nil {
		runtime := now.Sub(*o.ClaimedAt)
		if runtime > time.Duration(*o.EffectiveMaxRuntimeSeconds)*time.Second {
			return "max_runtime_exceeded", lastProgress,
				"dispatch runtime exceeded effective max_runtime"
		}
	}
	if o.EffectiveMaxQuietPeriodSeconds != nil && *o.EffectiveMaxQuietPeriodSeconds > 0 {
		// @deliberate: quiet window measured from last_progress_at, or
		// claimed_at when progress has never been bumped — the row was
		// just claimed and the executor has not yet returned an
		// AwaitAsyncCallback round-trip.
		ref := lastProgress
		if ref.IsZero() && o.ClaimedAt != nil {
			ref = *o.ClaimedAt
		}
		if !ref.IsZero() {
			quiet := now.Sub(ref)
			if quiet > time.Duration(*o.EffectiveMaxQuietPeriodSeconds)*time.Second {
				return "executor_quiet", lastProgress,
					"dispatch quiet-window exceeded effective max_quiet_period"
			}
		}
	}
	return "", lastProgress, ""
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
		// @constraint: RequiredStores is left empty (the foundation tick does not have the in-memory template registry threaded through, and an empty RequiredStores trivially satisfies the supervisor-pool predicate RequiredStores ⊆ AcceptedStores). FrameID is sourced from the node row — every stale node is part of the in-flight frame (blessed-invariant 19); a nil frame_id here means the frame engine has not yet advanced this node's queued frame, so skip and re-evaluate next tick.
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
			// @constraint: defensive — a closed RunScope means the rendezvous fired while the sweep was preparing the dispatch; walker discipline per concept:run-scope is to skip silently.
			// @concept: run-scope
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
