// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type FrameState string

const (
	FrameStateQueued    FrameState = "queued"
	FrameStateRunning   FrameState = "running"
	FrameStateCompleted FrameState = "completed"
	FrameStateFailed    FrameState = "failed"
)

type FramePending struct {
	FrameID    shared.UUID
	InstanceID shared.UUID
}

type FrameQueuedReady struct {
	FrameID             shared.UUID
	InstanceID          shared.UUID
	TriggeringMessageID shared.UUID
}

type FrameStuck struct {
	FrameID        shared.UUID
	InstanceID     shared.UUID
	FrameTimeoutMs int64
}

type OrphanFrameDispatch struct {
	DispatchID shared.UUID
	ClaimedBy  string
	FrameID    shared.UUID
}

type FrameRow struct {
	FrameID             shared.UUID `json:"frame_id"`
	InstanceID          shared.UUID `json:"instance_id"`
	State               FrameState  `json:"state"`
	TriggeringMessageID shared.UUID `json:"triggering_message_id"`
	StartedAt           *time.Time  `json:"started_at,omitempty"`
	EndedAt             *time.Time  `json:"ended_at,omitempty"`
	LastProgressAt *time.Time `json:"last_progress_at,omitempty"`
	FrameTimeoutMs int64      `json:"frame_timeout_ms"`
}

type FrameRowWithMessage struct {
	FrameRow
	MessageType       string `json:"message_type"`
	MessageSender     string `json:"message_sender"`
	MessageSenderKind string `json:"message_sender_kind"`
}

type FrameListFilter struct {
	InstanceID          *shared.UUID
	State               FrameState
	TriggeringMessageID *shared.UUID
}

type FrameTable interface {
	ListRunningFramesNoPendingNodes(ctx context.Context, tx Tx) ([]FramePending, error)

	HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx Tx) (bool, error)

	MarkRunningFrameTerminal(ctx context.Context, frameID shared.UUID, finalState FrameState, tx Tx) (transitioned bool, err error)

	// @concept: instance
	MarkInstanceTerminatedIfDone(ctx context.Context, instanceID shared.UUID, tx Tx) error

	ListQueuedFramesReadyToStart(ctx context.Context, tx Tx) ([]FrameQueuedReady, error)

	PromoteQueuedFrameToRunning(ctx context.Context, frameID shared.UUID, tx Tx) (transitioned bool, err error)

	GetRunningFrameID(ctx context.Context, instanceID shared.UUID, tx Tx) (*shared.UUID, error)

	MarkSourceNodeStale(ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx Tx) (matched bool, err error)

	ListStuckRunningFrames(ctx context.Context, tx Tx) ([]FrameStuck, error)

	ListOrphanFrameDispatches(ctx context.Context, tx Tx) ([]OrphanFrameDispatch, error)

	LookupFrameTimeoutMs(ctx context.Context, instanceID shared.UUID, tx Tx) (frameTimeoutMs int64, err error)

	InsertFrame(ctx context.Context, instanceID, triggeringMessageID shared.UUID, frameTimeoutMs int64, tx Tx) (shared.UUID, error)

	ListForObservabilityWithMessage(ctx context.Context, filter FrameListFilter, pag ListPagination, tx Tx) (PaginatedListResult[FrameRowWithMessage], error)

	GetForObservabilityWithMessage(ctx context.Context, frameID shared.UUID, tx Tx) (*FrameRowWithMessage, error)

	ListForObservability(ctx context.Context, filter FrameListFilter, pag ListPagination, tx Tx) (PaginatedListResult[FrameRow], error)

	GetForObservability(ctx context.Context, frameID shared.UUID, tx Tx) (*FrameRow, error)

	RefreshProgress(ctx context.Context, frameID shared.UUID, tx Tx) error

	CountHeldFrames(ctx context.Context, tx Tx) (int, error)

	// @concept: frame trace retention. Default `recentFramesKept` = 100
	// (via cfg:retention.recent_frames_kept); default trailing window via
	// cfg:retention.trace_trailing.
	PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error)
}
