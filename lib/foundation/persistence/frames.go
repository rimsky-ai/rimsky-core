// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// FrameTable is the persistence surface the frame engine (graph/frame)
// talks to. Methods mirror the SQL operations in graph/frame/{engine,
// producer}.go; the engine itself stays in graph/frame/ and orchestrates
// these calls.
//
// Per spec §3.5.

// FrameState mirrors the rimsky_frames.state column.
type FrameState string

const (
	FrameStateQueued    FrameState = "queued"
	FrameStateRunning   FrameState = "running"
	FrameStateCompleted FrameState = "completed"
	FrameStateFailed    FrameState = "failed"
)

// FrameResolutionMode mirrors the per-template `frame_resolution` setting.
type FrameResolutionMode string

const (
	FrameResolutionModeCoalesce    FrameResolutionMode = "coalesce"
	FrameResolutionModeSerialQueue FrameResolutionMode = "serial_queue"
)

// FramePending identifies a running frame whose nodes have all left
// stale/running, returned by ListRunningFramesNoPendingNodes.
type FramePending struct {
	FrameID    shared.UUID
	InstanceID shared.UUID
}

// FrameQueuedReady identifies a queued frame ready to be promoted to
// running, returned by ListQueuedFramesReadyToStart. SourceNodeIDs is the
// list of node IDs that originated the frame.
type FrameQueuedReady struct {
	FrameID       shared.UUID
	InstanceID    shared.UUID
	SourceNodeIDs []shared.UUID
}

// FrameStuck identifies a running frame past its timeout with no claimed
// dispatches and at least one stale/running node, returned by
// ListStuckRunningFrames.
type FrameStuck struct {
	FrameID        shared.UUID
	InstanceID     shared.UUID
	FrameTimeoutMs int64
}

// OrphanFrameDispatch identifies a dispatch row whose owning frame has
// already reached terminal state, returned by ListOrphanFrameDispatches.
type OrphanFrameDispatch struct {
	DispatchID shared.UUID
	ClaimedBy  string
	FrameID    shared.UUID
}

// FrameRow is the observability projection of one rimsky_frames row.
// Used by the observability frames endpoint.
type FrameRow struct {
	FrameID        shared.UUID         `json:"frame_id"`
	InstanceID     shared.UUID         `json:"instance_id"`
	State          FrameState          `json:"state"`
	Mode           FrameResolutionMode `json:"mode"`
	StartedAt      *time.Time          `json:"started_at,omitempty"`
	EndedAt        *time.Time          `json:"ended_at,omitempty"`
	FrameTimeoutMs int64               `json:"frame_timeout_ms"`
}

// FrameListFilter is the observability browse filter.
type FrameListFilter struct {
	InstanceID *shared.UUID
	State      FrameState
}

// FrameTable is the rimsky_frames accessor.
type FrameTable interface {
	// ---- Frame-end detection (engine.runFrameEndDetection) ----

	// ListRunningFramesNoPendingNodes returns running frames whose nodes
	// in the same (instance_id, frame_id) scope are all out of
	// stale/running.
	ListRunningFramesNoPendingNodes(ctx context.Context, tx Tx) ([]FramePending, error)

	// HasFailedNode returns true when any rimsky_nodes row for the given
	// (instanceID, frameID) is in state='failed'.
	HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx Tx) (bool, error)

	// MarkRunningFrameTerminal flips a running frame to its terminal state
	// (completed|failed) and stamps ended_at=now(). Returns transitioned=true
	// when the row moved (i.e., it was still 'running'); false if another
	// replica beat us to it.
	MarkRunningFrameTerminal(ctx context.Context, frameID shared.UUID, finalState FrameState, tx Tx) (transitioned bool, err error)

	// MarkInstanceTerminatedIfDone sets rimsky_instances.terminated_at=now()
	// when the durable-by-default terminal predicate holds and terminated_at
	// IS NULL. The predicate fires ONLY for an instance created with
	// terminate_after_run = true (durable instances — the default — are never
	// touched), and never while any node_run is unresolved (stale, running,
	// or parked). Strict "run at most once more" semantics: it does NOT wait
	// for queued frames to drain, and reads nothing about sensors or
	// publisher-subscriptions. Idempotent set-once. Per concept:instance.
	MarkInstanceTerminatedIfDone(ctx context.Context, instanceID shared.UUID, tx Tx) error

	// ---- Advance queued (engine.runAdvanceQueued) ----

	// ListQueuedFramesReadyToStart returns at most one queued frame per
	// instance — the oldest queued — for instances that have no currently-
	// running frame.
	ListQueuedFramesReadyToStart(ctx context.Context, tx Tx) ([]FrameQueuedReady, error)

	// PromoteQueuedFrameToRunning flips a queued frame to running and
	// stamps started_at=now(). Returns transitioned=true when exactly one
	// row moved.
	PromoteQueuedFrameToRunning(ctx context.Context, frameID shared.UUID, tx Tx) (transitioned bool, err error)

	// MarkSourceNodeStale flips a frame's source node to stale-with-frame_id.
	// Accepts the in-bounds states only: fresh, failed, or stale-with-NULL-
	// frame_id. Returns matched=true when exactly one row moved; false when
	// the node is out of bounds (e.g., already running under a different
	// frame).
	MarkSourceNodeStale(ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx Tx) (matched bool, err error)

	// ---- Stuck-frame warning (engine.runWarnStuckFrames) ----

	// ListStuckRunningFrames returns running frames whose
	// last_progress_at is past their frame_timeout_ms with no claimed
	// dispatches and at least one stale/running node. The frame engine
	// emits a `frame.stuck.observed` slog warning per row and takes no
	// destructive action — the frame stays running, no nodes are failed,
	// the instance is not terminated.
	ListStuckRunningFrames(ctx context.Context, tx Tx) ([]FrameStuck, error)

	// ---- Orphan dispatch reaper (engine.runReapOrphanFrameDispatches) ----

	// ListOrphanFrameDispatches returns dispatch rows whose claim is
	// non-NULL but whose owning frame has reached terminal state.
	ListOrphanFrameDispatches(ctx context.Context, tx Tx) ([]OrphanFrameDispatch, error)

	// ---- Producer (frame.EnqueueOrCoalesce) ----

	// LookupFrameResolutionMode reads (frame_resolution, frame_timeout_ms) for the
	// instance's template. Returns ("", 0, sql.ErrNoRows) when the instance
	// is missing. Empty mode surfaces as a validation error in the caller.
	LookupFrameResolutionMode(ctx context.Context, instanceID shared.UUID, tx Tx) (mode FrameResolutionMode, frameTimeoutMs int64, err error)

	// EnqueueSerialFrame inserts a queued serial_queue frame with one
	// source node and returns the new frame_id.
	EnqueueSerialFrame(ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx Tx) (shared.UUID, error)

	// EnqueueCoalesceFrame inserts a queued coalesce frame, or appends the
	// source node to an existing pending coalesce row for the instance.
	// Returns the frame_id of the row that received the source.
	EnqueueCoalesceFrame(ctx context.Context, instanceID, sourceNodeID shared.UUID, frameTimeoutMs int64, tx Tx) (shared.UUID, error)

	// ---- Observability ----

	// ListForObservability returns frames matching filter, cursor-paginated
	// by created_at DESC. Used by the observability /v1/observability/frames
	// endpoint.
	ListForObservability(ctx context.Context, filter FrameListFilter, pag ListPagination, tx Tx) (PaginatedListResult[FrameRow], error)

	// GetForObservability returns one frame by id. Returns (nil, nil) when
	// the row does not exist.
	GetForObservability(ctx context.Context, frameID shared.UUID, tx Tx) (*FrameRow, error)

	// RefreshProgress updates rimsky_frames.last_progress_at to NOW() for
	// the given frame. Called by the node-state-transition write path on
	// every UpdateState that carries the frame's id, so the
	// frame_timeout_ms metric measures no-progress-in-window rather than
	// frame age.
	//
	// See .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md §7.
	RefreshProgress(ctx context.Context, frameID shared.UUID, tx Tx) error

	// CountHeldFrames returns the number of running frames that have at
	// least one parked rimsky_node_runs row attached via frame_id.
	// Mirrors the held-frame notion used by
	// /admin/diagnostics/held-frames. Used by the metrics gauge
	// refresher (`rimsky_held_frames`). Tx must be open.
	CountHeldFrames(ctx context.Context, tx Tx) (int, error)

	// PruneTraceForRetention deletes terminal frame ROWS (and, via the
	// frame→node_run ON DELETE CASCADE, their node_runs) that are older
	// than `cutoff` OR beyond the `recentFramesKept` most-recent terminal
	// frames per instance — the lesser-of bound. Only terminal frames
	// (state IN ('completed','failed')) are eligible; in-flight frames
	// (queued/running, including parked-held) are never reaped. This
	// subsumes the prior node-run-only prune: the frame row itself is
	// deleted now, and the cascade removes its runs.
	//
	// `recentFramesKept <= 0` disables the count bound; a zero `cutoff`
	// (time.Time{}) disables the time bound. With both disabled the method
	// is a no-op returning 0. Returns the number of frame rows deleted.
	//
	// This method touches frames + node_runs ONLY. The time-keyed event
	// logs (rimsky_events, rimsky_node_events) are reaped separately by
	// EventTable.DeleteOlderThan / NodeEventTable.DeleteOlderThan, since
	// those are keyed by time, not by frame, and carry no frame FK.
	//
	// concept:frame trace retention. Default `recentFramesKept` = 100 (via
	// cfg:retention.recent_frames_kept); default trailing window via
	// cfg:retention.trace_trailing.
	PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error)
}
