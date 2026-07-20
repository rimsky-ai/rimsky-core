// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	FrameStateRunning    = "running"
	FrameStateFailed     = "failed"
	FrameStateTerminated = "terminated"
	FrameStateCompleted  = "completed"
)

type FramePending struct {
	FrameID    shared.UUID
	InstanceID shared.UUID
}

type OrphanFrameDispatch struct {
	NodeRunID shared.UUID
	ClaimedBy string
	FrameID   shared.UUID
}

type FrameRow struct {
	FrameID             shared.UUID `json:"frame_id"`
	InstanceID          shared.UUID `json:"instance_id"`
	State               string      `json:"state"`
	TriggeringMessageID shared.UUID `json:"triggering_message_id"`
	RootRunScopeID      shared.UUID `json:"root_run_scope_id"`
	StartedAt           *time.Time  `json:"started_at,omitempty"`
	EndedAt             *time.Time  `json:"ended_at,omitempty"`
	LastProgressAt      *time.Time  `json:"last_progress_at,omitempty"`
}

type FrameRowWithMessage struct {
	FrameRow
	MessageType       string `json:"message_type"`
	MessageSender     string `json:"message_sender"`
	MessageSenderKind string `json:"message_sender_kind"`
}

type FrameListFilter struct {
	InstanceID          *shared.UUID
	Unresolved          *bool
	TerminalState       *string
	TriggeringMessageID *shared.UUID
}

func ApplyFrameStateQueryParam(filter *FrameListFilter, s string) error {
	switch s {
	case "":
		return nil
	case FrameStateRunning:
		unresolved := true
		filter.Unresolved = &unresolved
		return nil
	case FrameStateFailed, FrameStateTerminated, FrameStateCompleted:
		state := s
		filter.TerminalState = &state
		return nil
	default:
		return fmt.Errorf("state %q invalid (want one of %s, %s, %s, %s)",
			s, FrameStateRunning, FrameStateFailed, FrameStateTerminated, FrameStateCompleted)
	}
}

type FrameTable interface {
	ListRunningFramesNoPendingNodes(ctx context.Context, tx Tx) ([]FramePending, error)

	HasFailedNode(ctx context.Context, instanceID, frameID shared.UUID, tx Tx) (bool, error)

	MarkFrameEnded(ctx context.Context, frameID shared.UUID, tx Tx) (transitioned bool, err error)

	// @concept: frame
	MarkOpenFramesEndedForInstance(ctx context.Context, instanceID shared.UUID, tx Tx) (int, error)

	EndFrameIfSettled(ctx context.Context, frameID shared.UUID, tx Tx) (transitioned bool, err error)

	GetRunningFrameID(ctx context.Context, instanceID shared.UUID, tx Tx) (*shared.UUID, error)

	MarkSourceNodeStale(ctx context.Context, instanceID, nodeID, frameID shared.UUID, tx Tx) (matched bool, err error)

	ListOrphanFrameDispatches(ctx context.Context, tx Tx) ([]OrphanFrameDispatch, error)

	// @decision: empty-message-as-root-trigger
	InsertRunningFrame(ctx context.Context, instanceID, triggeringMessageID, rootRunScopeID shared.UUID, tx Tx) (shared.UUID, error)

	ListForObservabilityWithMessage(ctx context.Context, filter FrameListFilter, pag ListPagination, tx Tx) (PaginatedListResult[FrameRowWithMessage], error)

	GetForObservabilityWithMessage(ctx context.Context, frameID shared.UUID, tx Tx) (*FrameRowWithMessage, error)

	ListForObservability(ctx context.Context, filter FrameListFilter, pag ListPagination, tx Tx) (PaginatedListResult[FrameRow], error)

	GetForObservability(ctx context.Context, frameID shared.UUID, tx Tx) (*FrameRow, error)

	CountHeldFrames(ctx context.Context, tx Tx) (int, error)

	// @concept: frame
	PruneTraceForRetention(ctx context.Context, recentFramesKept int, cutoff time.Time) (int, error)
}
