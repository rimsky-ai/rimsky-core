// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// FrameStore is the persistence surface the frame engine (modeling/frame)
// talks to. Methods mirror the SQL operations in modeling/frame/{engine,
// producer}.go; the engine itself stays in modeling/frame/ and orchestrates
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

// FrameMode mirrors the per-template `frame_resolution` setting.
type FrameMode string

const (
	FrameModeCoalesce    FrameMode = "coalesce"
	FrameModeSerialQueue FrameMode = "serial_queue"
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
	FrameID        shared.UUID `json:"frame_id"`
	InstanceID     shared.UUID `json:"instance_id"`
	State          FrameState  `json:"state"`
	Mode           FrameMode   `json:"mode"`
	StartedAt      *time.Time  `json:"started_at,omitempty"`
	EndedAt        *time.Time  `json:"ended_at,omitempty"`
	FrameTimeoutMs int64       `json:"frame_timeout_ms"`
}

// FrameListFilter is the observability browse filter.
type FrameListFilter struct {
	InstanceID *shared.UUID
	State      FrameState
}

// FrameStore is the rimsky_frames accessor.
type FrameStore interface {
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
	// when the terminal predicate holds (no queued/running frames, no
	// stale/running nodes for the instance) and terminated_at IS NULL.
	// Idempotent set-once.
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

	// ---- Stuck frame reaper (engine.runReapStuckFrames) ----

	// ListStuckRunningFrames returns running frames past their timeout
	// with no claimed dispatches and at least one stale/running node.
	ListStuckRunningFrames(ctx context.Context, tx Tx) ([]FrameStuck, error)

	// FailAllPendingNodes flips every stale/running node for the instance
	// to state='failed' and stamps updated_at=now().
	FailAllPendingNodes(ctx context.Context, instanceID shared.UUID, tx Tx) error

	// ---- Orphan dispatch reaper (engine.runReapOrphanFrameDispatches) ----

	// ListOrphanFrameDispatches returns dispatch rows whose claim is
	// non-NULL but whose owning frame has reached terminal state.
	ListOrphanFrameDispatches(ctx context.Context, tx Tx) ([]OrphanFrameDispatch, error)

	// ---- Producer (frame.EnqueueOrCoalesce) ----

	// LookupFrameMode reads (frame_resolution, frame_timeout_ms) for the
	// instance's template. Returns ("", 0, sql.ErrNoRows) when the instance
	// is missing. Empty mode surfaces as a validation error in the caller.
	LookupFrameMode(ctx context.Context, instanceID shared.UUID, tx Tx) (mode FrameMode, frameTimeoutMs int64, err error)

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
}
