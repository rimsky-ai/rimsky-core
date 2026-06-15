// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// Logger is the minimum logging surface RunTick needs. Both
// *log/slog.Logger and shared.Logger (the scheduler's structured-log
// wrapper) satisfy this; keeping it tiny lets graph/frame avoid
// importing graph/shared without losing the scheduler's pre-bound
// fields when the scheduler wires its logger through.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// MetricsHook is the minimum metrics surface frame.RunTick needs.
// Foundation/integration's MetricsHook structurally satisfies this so
// the scheduler can pass through its registry adapter without forcing
// graph/frame to import runtime.
type MetricsHook interface {
	ObserveFrameDuration(seconds float64)
}

// RunTick performs one frame-engine iteration. The caller must hold the
// scheduler-tick advisory lock (blessed-invariant 7).
//
// Steps per §4.1 of the spec:
//  1. Detect frame-end (transition running → completed|failed).
//  2. Advance queued — promote oldest queued to running.
//  3. Warn on stuck frames (timeout exceeded with no claimed dispatches) — observation only, not destructive.
//  4. Reap orphan dispatches (frame in terminal state but dispatch still claimed).
//
// Each step opens its own short tx so partial failures don't poison the
// whole tick. The advisory lock guarantees serialization across replicas;
// within one process this is just a sequential loop.
func RunTick(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger, metrics ...MetricsHook) error {
	var m MetricsHook
	if len(metrics) > 0 {
		m = metrics[0]
	}
	if err := runFrameEndDetection(ctx, store, logger, m); err != nil {
		return fmt.Errorf("frame.RunTick: frame-end: %w", err)
	}
	if err := runAdvanceQueued(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: advance: %w", err)
	}
	if err := runWarnStuckFrames(ctx, store, logger); err != nil {
		return fmt.Errorf("frame.RunTick: warn stuck: %w", err)
	}
	if err := runReapOrphanFrameDispatches(ctx, store, queue, logger); err != nil {
		return fmt.Errorf("frame.RunTick: reap orphan: %w", err)
	}
	return nil
}

func runFrameEndDetection(ctx context.Context, store persistence.Tables, logger Logger, metrics MetricsHook) error {
	// @deliberate: Step 1: collect pendings outside any subsequent transition tx so a
	// single bad frame doesn't poison the whole tick.
	var pendings []persistence.FramePending
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ps, err := store.Frames().ListRunningFramesNoPendingNodes(ctx, tx)
		if err != nil {
			return err
		}
		pendings = ps
		return nil
	}); err != nil {
		return err
	}

	// @deliberate: per-frame transition tx so a single frame's failure
	// leaves the rest unaffected.
	for _, p := range pendings {
		if err := transitionFrameEnd(ctx, store, p.FrameID, p.InstanceID, logger, metrics); err != nil {
			logger.Warn("frame.end.transition_failed",
				"frame_id", p.FrameID,
				"instance_id", p.InstanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

// transitionFrameEnd applies one frame's running → completed|failed
// transition in its own short tx. After the frame transitions to
// terminal, the same tx evaluates the instance's terminal predicate
// and sets rimsky_instances.terminated_at if satisfied
// (control-plane spec §2.4: idempotent, set-once).
func transitionFrameEnd(ctx context.Context, store persistence.Tables, frameID, instanceID shared.UUID, logger Logger, metrics MetricsHook) error {
	var transitioned bool
	var finalState persistence.FrameState
	var startedAt, endedAt *time.Time
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		anyFailed, err := store.Frames().HasFailedNode(ctx, instanceID, frameID, tx)
		if err != nil {
			return err
		}
		finalState = persistence.FrameStateCompleted
		if anyFailed {
			finalState = persistence.FrameStateFailed
		}
		// @deliberate: Snapshot started_at before MarkRunningFrameTerminal stamps
		// MarkRunningFrameTerminal stamps ended_at = now() so the metric
		// observes the running window without a second roundtrip.
		row, gerr := store.Frames().GetForObservability(ctx, frameID, tx)
		if gerr != nil {
			return gerr
		}
		moved, err := store.Frames().MarkRunningFrameTerminal(ctx, frameID, finalState, tx)
		if err != nil {
			return err
		}
		transitioned = moved
		if moved && row != nil {
			startedAt = row.StartedAt
			// @deliberate: use clock-now for ended_at; the SQL stamps
			// now() in the same tx so this is equivalent to the persisted
			// value.
			now := time.Now()
			endedAt = &now
		}
		return store.Frames().MarkInstanceTerminatedIfDone(ctx, instanceID, tx)
	}); err != nil {
		return err
	}
	if transitioned {
		logger.Info("frame.end",
			"frame_id", frameID,
			"instance_id", instanceID,
			"final_state", finalState)
		if metrics != nil && startedAt != nil && endedAt != nil {
			metrics.ObserveFrameDuration(endedAt.Sub(*startedAt).Seconds())
		}
	}
	return nil
}

func runAdvanceQueued(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger) error {
	var advances []persistence.FrameQueuedReady
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		as, err := store.Frames().ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		advances = as
		return nil
	}); err != nil {
		return err
	}

	for _, a := range advances {
		if err := advanceOneFrame(ctx, store, queue, a.FrameID, a.InstanceID, a.TriggeringMessageID, logger); err != nil {
			logger.Warn("frame.start.advance_failed",
				"frame_id", a.FrameID,
				"instance_id", a.InstanceID,
				"error", err.Error())
			continue
		}
	}
	return nil
}

// advanceOneFrame promotes one queued frame to running and stale-marks
// every wake-target named in the triggering message's payload.
//
// Pass 4 of the 2026-06-14 message-schema-layer reshape: the legacy
// rimsky_frames.source_node_ids column retired (Pass 1) and with it the
// frame-engine path that stale-marked source nodes at promotion. The
// replacement is `payload.wake_node_ids` on the triggering message — an
// array of node-UUIDs the runtime-side (instance-factory for roots,
// invalidate / reset handlers for ad-hoc invalidates) embeds when it
// seeds the synthetic envelope. Reading the array at promotion preserves
// the promote+stale-mark atomicity the supervisor's `ListReadyForDispatch`
// relies on: the supervisor never sees a stale row whose frame is still
// queued (the cheaper shape "stale-mark at instance-factory time" admits
// dispatch against a queued frame, breaking the frame-end invariant).
//
// Two wake mechanisms by design (the divide is structural, not transitional):
//
//   - Runtime-synthetic envelopes (the ones THIS function looks at) wake
//     receivers by enumerating node-UUIDs in `payload.wake_node_ids`. Emit
//     sites construct these envelopes via `runtime.EnqueueSyntheticWakeFrame`
//     — the instance-factory's initial-frame seed
//     (`code:control/controlapi/instances.go::createInstance`, type
//     `"instance/root"`), the reset handler's next-frame seed
//     (`code:control/controlapi/nodes.go::handleResetNode`, type
//     `"node/reset"`), and the asset-materialize handler
//     (`code:control/controlapi/assets.go`, type `"asset/materialize"`).
//     None of these types is declared in any template's `messages:`
//     registry — they bypass the registry gate by going through
//     `runtime.EnqueueMessage` directly. Receivers are addressed by UUID,
//     not by subscription.
//
//   - Author-declared envelopes (operator-posted, publisher-emitted,
//     cascade-emitted from a message-emitter node) carry NO
//     `wake_node_ids`. Receivers wake through the subscriber-side cascade
//     in `runtime.cascadeMessageVirtualNodeSettleInTx`: the message's
//     `type` is the virtual-node sender key, subscribers declared via
//     `subscribes: [{node: <type>, type: terminal/success}]` match through
//     the standard edge map and stale-mark in the new frame.
//
// Template authors do NOT subscribe to the synthetic types — those
// envelopes ship without `wake_node_ids` only by accident; a template
// author who writes `subscribes: [{node: instance/root, type: terminal/
// success}]` would discover the synthetic surface, but the registry
// validator would reject `instance/root` as undeclared. The divide is
// stable as long as runtime-synthetic types are confined to runtime-
// internal call sites.
//
// @deliberate: runtime-synthetic envelopes wake via
// payload.wake_node_ids; author-declared envelopes wake via the
// subscriber-side cascade. Both surfaces converge on the same stale-
// mark + dispatch path; only the wake-target selection differs.
//
// @blessed-invariant 21: messages are inert. The `json.Unmarshal`
// below is the fifth sanctioned site that reads payload bytes — see
// the enumeration in
// `code:graph/attribute/substitution.go::resolveTriggerValue` and
// `code:graph/attribute/substitution.go::resolveMessagesValue`. The
// read is runtime-internal wake-field extraction (the rimsky-synthesized
// `wake_node_ids` array) and a different surface than user-authored
// body reads. The bytes are never logged, formatted with `%v`, or
// echoed into error messages; an unparseable payload only emits a
// warn with the frame_id + triggering_message_id + the decode error
// string (never payload contents).
func advanceOneFrame(
	ctx context.Context, store persistence.Tables, queue persistence.Queue, frameID, instanceID, triggeringMessageID uuid.UUID, logger Logger,
) error {
	var promoted bool
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		moved, err := store.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if !moved {
			// @deliberate: Another replica won; nothing to do.
			return nil
		}
		promoted = true
		// @deliberate: Read the triggering message and stale-mark every
		// wake_node_ids target IN THE PROMOTION TX. Co-committing
		// guarantees the supervisor cannot observe a stale row before
		// its frame transitions to running. The tx-aware GetInTx is
		// mandatory here: the tx-less Get goes through the pool
		// (db.QueryContext), and under SQLite's MaxOpenConns=1 that
		// blocks forever waiting for the only pool connection — held
		// by this open promotion tx. See @blessed-invariant: tx-aware
		// reads from inside an open tx.
		msg, err := store.Messages().GetInTx(ctx, tx, shared.UUID(triggeringMessageID))
		if err != nil {
			return fmt.Errorf("advanceOneFrame: get triggering message: %w", err)
		}
		if msg == nil || len(msg.Payload) == 0 {
			return nil
		}
		var payloadMap map[string]any
		if err := json.Unmarshal(msg.Payload, &payloadMap); err != nil {
			// @deliberate: malformed payload — log and proceed. The frame is
			// running; subscriber-side cascade is still available for
			// other wake mechanisms.
			logger.Warn("advanceOneFrame: malformed message payload",
				"frame_id", frameID,
				"triggering_message_id", triggeringMessageID,
				"error", err.Error())
			return nil
		}
		ids, _ := payloadMap["wake_node_ids"].([]any)
		if len(ids) == 0 {
			return nil
		}
		inst, err := store.Instances().Get(ctx, shared.UUID(instanceID), tx)
		if err != nil {
			return fmt.Errorf("advanceOneFrame: get instance: %w", err)
		}
		if inst == nil {
			return nil
		}
		// @deliberate: capture per-node-id the affirmed in-flight run id
		// so the upstream-refresh wait-set pre-install below can map
		// (receiver_id, upstream_id) pairs into their run ids without a
		// second resolver loop.
		runIDByNode := make(map[shared.UUID]shared.UUID, len(ids))
		for _, raw := range ids {
			s, ok := raw.(string)
			if !ok {
				continue
			}
			parsed, perr := uuid.Parse(s)
			if perr != nil {
				continue
			}
			nodeID := shared.UUID(parsed)
			node, gerr := store.Nodes().Get(ctx, nodeID, tx)
			if gerr != nil {
				return fmt.Errorf("advanceOneFrame: get wake node: %w", gerr)
			}
			if node == nil {
				continue
			}
			scope := inst.MainRunScopeID
			if node.RunScopeID != nil {
				scope = *node.RunScopeID
			}
			if err := store.Nodes().AffirmNodeRunRow(ctx, nodeID, scope, shared.UUID(frameID), tx); err != nil {
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					continue
				}
				return fmt.Errorf("advanceOneFrame: affirm wake node: %w", err)
			}
			runID, hasInFlight, err := queue.GetInFlightRunForNode(ctx, tx, nodeID, scope)
			if err != nil {
				return fmt.Errorf("advanceOneFrame: resolve wake run: %w", err)
			}
			if !hasInFlight {
				continue
			}
			if err := store.Nodes().MarkStaleForCascade(ctx, runID, shared.UUID(frameID), tx); err != nil {
				return fmt.Errorf("advanceOneFrame: stale-mark wake node: %w", err)
			}
			runIDByNode[nodeID] = runID
		}
		// @blessed-invariant: upstream-staled-before-receiver-dispatch
		// — pre-install wait-set rows for every `wait_set_pairs` entry
		// the synthetic envelope embedded. Each pair maps a receiver to
		// a force_upstream_refresh upstream the receiver depends on;
		// both are in this frame's wake_node_ids (the synthetic-envelope
		// chokepoint auto-expanded them). The pre-installed wait-set
		// row keys (frame, receiver_run, upstream_run) so the
		// supervisor's existing eligibility predicate gates the receiver
		// until the upstream settles + drains its wait-set; the cascade
		// walker drains the row in the upstream's terminal tx; the
		// substitution context builder reads the drained row at the
		// receiver's dispatch — all existing machinery, no race window
		// between stale-mark and gate install.
		// @story: upstream-pull-on-invalidate
		// @concept: wait-set
		// @concept: cascade
		if pairs, ok := payloadMap["wait_set_pairs"].([]any); ok {
			for _, raw := range pairs {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				rStr, _ := m["receiver"].(string)
				uStr, _ := m["upstream"].(string)
				rUUID, rerr := uuid.Parse(rStr)
				uUUID, uerr := uuid.Parse(uStr)
				if rerr != nil || uerr != nil {
					continue
				}
				receiverRun, rOK := runIDByNode[shared.UUID(rUUID)]
				upstreamRun, uOK := runIDByNode[shared.UUID(uUUID)]
				if !rOK || !uOK {
					continue
				}
				if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
					FrameID:           shared.UUID(frameID),
					ReceiverRunID:     receiverRun,
					SenderRunID:       upstreamRun,
					TopicKind:         "attribute",
					SubscriptionScope: "direct",
				}, tx); err != nil {
					return fmt.Errorf("advanceOneFrame: install upstream-refresh wait-set: %w", err)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if promoted {
		logger.Info("frame.start",
			"frame_id", frameID,
			"instance_id", instanceID,
			"triggering_message_id", triggeringMessageID)
	}
	return nil
}

// runWarnStuckFrames observes frames that have made no progress within
// their `frame_timeout_ms` window and emits a single `frame.stuck.observed`
// slog warning per such frame. It does NOT take destructive action:
// the frame stays `running`, no nodes are failed, the instance is not
// terminated. Operators are expected to investigate via the dashboard /
// event log and decide whether to issue an operator invalidate, mark a
// node failed manually, or wait. (Pre-v1 design choice: no blanket
// "frame too old; kill it" policy.)
func runWarnStuckFrames(ctx context.Context, store persistence.Tables, logger Logger) error {
	var stuckFrames []persistence.FrameStuck
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ss, err := store.Frames().ListStuckRunningFrames(ctx, tx)
		if err != nil {
			return err
		}
		stuckFrames = ss
		return nil
	}); err != nil {
		return err
	}

	for _, s := range stuckFrames {
		logger.Warn("frame.stuck.observed",
			"frame_id", s.FrameID,
			"instance_id", s.InstanceID,
			"timeout_ms", s.FrameTimeoutMs)
	}
	return nil
}

// runReapOrphanFrameDispatches releases dispatch claims whose frame has
// already reached a terminal state. Per blessed-invariant 4 (claimant-
// guarded release), the per-row UPDATE filters by `claimed_by =
// supervisor_id` so a fresh supervisor that re-claimed the row keeps
// its live claim.
func runReapOrphanFrameDispatches(ctx context.Context, store persistence.Tables, queue persistence.Queue, logger Logger) error {
	var orphans []persistence.OrphanFrameDispatch
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		os, err := store.Frames().ListOrphanFrameDispatches(ctx, tx)
		if err != nil {
			return err
		}
		orphans = os
		return nil
	}); err != nil {
		return err
	}

	for _, o := range orphans {
		// @deliberate: Queue.ReleaseClaim auto-commits its own tx;
		// claimant-guarded by expectedClaimedBy.
		if err := queue.ReleaseClaim(ctx, o.DispatchID, o.ClaimedBy); err != nil {
			logger.Warn("frame.orphan_dispatch.release_failed",
				"dispatch_id", o.DispatchID,
				"frame_id", o.FrameID,
				"prior_claimed_by", o.ClaimedBy,
				"error", err.Error())
			continue
		}
		logger.Warn("frame.orphan_dispatch.reaped",
			"dispatch_id", o.DispatchID,
			"frame_id", o.FrameID,
			"prior_claimed_by", o.ClaimedBy)
	}
	return nil
}
