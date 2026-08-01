// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: cascade
type PureCascadeReadyRow struct {
	NodeID     shared.UUID
	InstanceID shared.UUID
	NodeType   string
	NodeRunID  shared.UUID
	RunScopeID shared.UUID
	FrameID    shared.UUID
}

// @concept: node
type NodeRunSummary struct {
	ActiveCount  int `json:"active_count"`
	PendingCount int `json:"pending_count"`
	FreshCount   int `json:"fresh_count"`
	FailedCount  int `json:"failed_count"`
}

// @concept: node
// @decision: frame-isolation-is-structural
type NodeRow struct {
	ID         shared.UUID `json:"id"`
	InstanceID shared.UUID `json:"instance_id"`
	NodeType   string      `json:"node_type"`
	Executor   string      `json:"executor"`
	// @concept: node
	Tags []string `json:"tags"`
	// @concept: cascade
	// @decision: mode-default-most-recent
	CascadeMode cascade.CascadeMode `json:"cascade_mode"`
	CreatedAt   time.Time           `json:"created_at"`
}

type NodeCreateInput struct {
	ID         shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string
	Tags       []string
	// @concept: cascade
	// @decision: mode-default-most-recent
	CascadeMode cascade.CascadeMode
}

type NodeListFilter struct {
	Tag string
}

type NodeTable interface {
	Create(ctx context.Context, in NodeCreateInput, tx Tx) (NodeRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*NodeRow, error)
	ListByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]NodeRow, error)
	ListByInstancePagedFiltered(ctx context.Context, instanceID shared.UUID, pag ListPagination, filter NodeListFilter, tx Tx) (PaginatedListResult[NodeRow], error)
	ListReadyForDispatch(ctx context.Context, tx Tx) ([]NodeRow, error)
	// @concept: supervisor
	CountRunningForSupervisor(ctx context.Context, supervisorID string, tx Tx) (int, error)
	// @concept: node
	CountAllNodes(ctx context.Context, tx Tx) (int, error)
	// @concept: node
	CountDistinctNodesWithRuns(ctx context.Context, tx Tx) (int, error)
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]PureCascadeReadyRow, error)
	CountByState(ctx context.Context, tx Tx) (map[cascade.NodeState]int, error)
	UpdateState(ctx context.Context, nodeRunID shared.UUID, state cascade.NodeState, reason cascade.TransitionReason, settlingSignalType *string, tx Tx) error
	// @concept: error-policy
	UpdateRunEvaluatorState(ctx context.Context, runID shared.UUID, es spec.EvaluatorState, tx Tx) error
	// @concept: error-policy
	GetRunEvaluatorState(ctx context.Context, runID shared.UUID, tx Tx) (spec.EvaluatorState, error)
	ResetFailedTerminalSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error

	// @concept: run-scope
	GetFailedTerminalRunScopeID(ctx context.Context, id shared.UUID, tx Tx) (*shared.UUID, error)

	// @concept: signal
	HasRunForNodeInFrame(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (bool, error)

	// @concept: cascade
	// @concept: run-scope
	HasAdvancedSiblingInScope(ctx context.Context, nodeID, runScopeID, excludingRunID shared.UUID, tx Tx) (bool, error)

	// @concept: cascade
	// @concept: run-scope
	ListPendingSiblingRunsInScope(ctx context.Context, senderNodeRunID shared.UUID, tx Tx) ([]shared.UUID, error)

	// @concept: cascade
	// @concept: run-scope
	ListPendingRunsInScopeForNodes(ctx context.Context, runScopeID shared.UUID, nodeIDs []shared.UUID, tx Tx) ([]shared.UUID, error)

	// @concept: cascade
	// @decision: mode-default-most-recent
	HasLaterCascadePending(ctx context.Context, nodeID, runScopeID shared.UUID, afterSeq int64, tx Tx) (bool, error)

	GetRunByDispatchIDForUpdate(ctx context.Context, dispatchNodeRunID shared.UUID, tx Tx) (*NodeRunForCallback, error)

	// @concept: cascade
	// @decision: mode-default-most-recent
	GetCascadeMode(ctx context.Context, nodeID shared.UUID, tx Tx) (cascade.CascadeMode, error)

	// @concept: node
	GetRunSummary(ctx context.Context, nodeID shared.UUID, tx Tx) (NodeRunSummary, error)

	// @concept: node
	GetRunSummaryForNodes(ctx context.Context, nodeIDs []shared.UUID, tx Tx) (map[shared.UUID]NodeRunSummary, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	FindLatestCascadePending(ctx context.Context, nodeID, runScopeID, frameID shared.UUID, tx Tx) (*NodeRunForGate, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	CreateCascadePending(ctx context.Context, nodeID, runScopeID, frameID shared.UUID, tx Tx) (shared.UUID, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	LockReceiverCascade(ctx context.Context, nodeID, runScopeID, frameID shared.UUID, tx Tx) error

	// @concept: cascade
	GetRunForGate(ctx context.Context, runID shared.UUID, tx Tx) (*NodeRunForGate, error)

	// @concept: node-run
	GetLatestRunForNode(ctx context.Context, nodeID shared.UUID, tx Tx) (*NodeRunLatest, error)

	// @concept: node-run
	GetLatestRunForNodes(ctx context.Context, nodeIDs []shared.UUID, tx Tx) (map[shared.UUID]NodeRunLatest, error)

	// @concept: node-run
	ListRunsForInstanceByStates(ctx context.Context, instanceID shared.UUID, states []cascade.NodeState, tx Tx) ([]NodeRunLatest, error)

	// @concept: cascade
	// @concept: run-scope
	GetPriorRunBySequence(ctx context.Context, nodeID, runScopeID shared.UUID, beforeSeq int64, tx Tx) (*NodeRunForGate, error)

	// @concept: cascade
	// @decision: mode-default-most-recent
	DeletePriorCascadeStales(ctx context.Context, nodeID, runScopeID shared.UUID, beforeSeq int64, tx Tx) (int, error)

	// @concept: cascade
	GetPriorCascadeQueuedNotClaimed(ctx context.Context, nodeID, runScopeID shared.UUID, beforeSeq int64, tx Tx) (*NodeRunForGate, error)

	// @concept: cascade
	GetMostRecentSettledRun(ctx context.Context, nodeID, runScopeID shared.UUID, beforeSeq int64, tx Tx) (*NodeRunForGate, error)

	// @concept: cascade
	TransitionPendingToStale(ctx context.Context, runID shared.UUID, enqueuedAt time.Time, tx Tx) error

	// @concept: cascade
	SetRunRequiredClaimProducers(ctx context.Context, runID shared.UUID, requiredClaimProducers []string, tx Tx) (bool, error)

	// @concept: cascade
	DropPendingRun(ctx context.Context, runID shared.UUID, tx Tx) error

	// @concept: cascade
	// @decision: non-cascade-direct-to-stale
	CreateNonCascadeStale(ctx context.Context, in NonCascadeStaleInput, tx Tx) (shared.UUID, error)
}

// @concept: node-run
type NodeRunLatest struct {
	NodeRunID          shared.UUID
	NodeID             shared.UUID
	RunScopeID         shared.UUID
	FrameID            shared.UUID
	State              cascade.NodeState
	SettlingSignalType *string
	ClaimedBy          string
	Sequence           int64
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
type NodeRunForGate struct {
	NodeRunID      shared.UUID
	NodeID         shared.UUID
	RunScopeID     shared.UUID
	FrameID        shared.UUID
	Sequence       int64
	State          cascade.NodeState
	CreationReason cascade.CreationReason
	ClaimedBy      string
}

// @concept: cascade
// @decision: non-cascade-direct-to-stale
type NonCascadeStaleInput struct {
	NodeID                      shared.UUID
	RunScopeID                  shared.UUID
	FrameID                     shared.UUID
	ExecutorName                string
	RequiredClaimProducers      []string
	EnqueuedAt                  time.Time
	CreationReason              cascade.CreationReason
	PriorNodeRunID              *shared.UUID
	PriorDispatchDisposition    string
	InitialScratchInline        []byte
	InitialScratchHandle        string
	InitialScratchHandleBackend string
}

type NodeRunForCallback struct {
	ID         shared.UUID
	NodeID     shared.UUID
	RunScopeID shared.UUID
	FrameID    shared.UUID
	State      cascade.NodeState
}
