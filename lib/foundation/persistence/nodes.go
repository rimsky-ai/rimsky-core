// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	RunID      shared.UUID
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
type NodeRow struct {
	ID         shared.UUID  `json:"id"`
	InstanceID shared.UUID  `json:"instance_id"`
	NodeType   string       `json:"node_type"`
	Executor   string       `json:"executor"`
	FrameID    *shared.UUID `json:"frame_id,omitempty"`
	// @concept: node
	Tags []string `json:"tags"`
	// @concept: cascade
	// @decision: mode-default-most-recent
	CascadeMode cascade.CascadeMode `json:"cascade_mode"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

type NodeCreateInput struct {
	ID         shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string
	Tags       []string
	// @concept: cascade
	// @decision: mode-default-most-recent
	CascadeMode string
}

type NodeListFilter struct {
	Tag string
}

type NodeTable interface {
	Create(ctx context.Context, in NodeCreateInput, tx Tx) (NodeRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*NodeRow, error)
	ListByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]NodeRow, error)
	ListByInstancePaged(ctx context.Context, instanceID shared.UUID, pag ListPagination, tx Tx) (PaginatedListResult[NodeRow], error)
	ListByInstancePagedFiltered(ctx context.Context, instanceID shared.UUID, pag ListPagination, filter NodeListFilter, tx Tx) (PaginatedListResult[NodeRow], error)
	ListReadyForDispatch(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListRunning(ctx context.Context, tx Tx) ([]NodeRow, error)
	// @concept: supervisor
	CountRunningForSupervisor(ctx context.Context, supervisorID string, tx Tx) (int, error)
	// @concept: node
	CountAllNodes(ctx context.Context, tx Tx) (int, error)
	// @concept: node
	CountDistinctNodesWithRuns(ctx context.Context, tx Tx) (int, error)
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]PureCascadeReadyRow, error)
	CountByState(ctx context.Context, tx Tx) (map[cascade.NodeState]int, error)
	UpdateState(ctx context.Context, id shared.UUID, runScopeID shared.UUID, state cascade.NodeState, reason cascade.TransitionReason, settlingSignalType *string, tx Tx) error
	// @concept: error-policy
	UpdateRunEvaluatorState(ctx context.Context, runID shared.UUID, es spec.EvaluatorState, tx Tx) error
	// @concept: error-policy
	GetRunEvaluatorState(ctx context.Context, runID shared.UUID, tx Tx) (spec.EvaluatorState, error)
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	ClearSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error
	ResetFailedTerminalSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error

	// @concept: run-scope
	GetFailedTerminalRunScopeID(ctx context.Context, id shared.UUID, tx Tx) (*shared.UUID, error)

	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
	// @concept: signal
	HasRunForNodeInFrame(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (bool, error)

	// @concept: cascade
	// @concept: run-scope
	HasAdvancedSiblingInScope(ctx context.Context, tx Tx, nodeID, runScopeID, excludingRunID shared.UUID) (bool, error)

	// @concept: cascade
	// @concept: run-scope
	ListPendingSiblingRunsInScope(ctx context.Context, tx Tx, senderRunID shared.UUID) ([]shared.UUID, error)

	// @concept: cascade
	// @concept: run-scope
	ListPendingRunsInScopeForNodes(ctx context.Context, tx Tx, runScopeID shared.UUID, nodeIDs []shared.UUID) ([]shared.UUID, error)

	// @concept: cascade
	// @decision: mode-default-most-recent
	HasLaterCascadePending(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID, afterSeq int64) (bool, error)

	GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID shared.UUID, tx Tx) (*NodeRunForCallback, error)

	// @concept: cascade
	// @decision: mode-default-most-recent
	GetCascadeMode(ctx context.Context, nodeID shared.UUID, tx Tx) (cascade.CascadeMode, error)

	// @concept: node
	GetRunSummary(ctx context.Context, nodeID shared.UUID, tx Tx) (NodeRunSummary, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	FindLatestCascadePending(ctx context.Context, tx Tx, nodeID, runScopeID, frameID shared.UUID) (*NodeRunForGate, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	CreateCascadePending(ctx context.Context, tx Tx, nodeID, runScopeID, frameID shared.UUID) (shared.UUID, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	LockReceiverCascade(ctx context.Context, tx Tx, nodeID, runScopeID, frameID shared.UUID) error

	// @concept: cascade
	GetRunForGate(ctx context.Context, tx Tx, runID shared.UUID) (*NodeRunForGate, error)

	// @concept: node-run
	GetLatestRunForNode(ctx context.Context, tx Tx, nodeID shared.UUID) (*NodeRunLatest, error)

	// @concept: node-run
	GetLatestRunInScope(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID) (*NodeRunLatest, error)

	// @concept: node-run
	ListRunsForInstanceByStates(ctx context.Context, tx Tx, instanceID shared.UUID, states []cascade.NodeState) ([]NodeRunLatest, error)

	// @concept: cascade
	// @concept: run-scope
	GetPriorRunBySequence(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID, beforeSeq int64) (*NodeRunForGate, error)

	// @concept: cascade
	// @decision: mode-default-most-recent
	DeletePriorCascadeStales(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID, beforeSeq int64) (int, error)

	// @concept: cascade
	GetPriorCascadeStaleNotClaimed(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID, beforeSeq int64) (*NodeRunForGate, error)

	// @concept: cascade
	GetMostRecentSettledRun(ctx context.Context, tx Tx, nodeID, runScopeID shared.UUID, beforeSeq int64) (*NodeRunForGate, error)

	// @concept: cascade
	TransitionPendingToStale(ctx context.Context, tx Tx, runID shared.UUID, enqueuedAt time.Time) error

	// @concept: cascade
	DropPendingRun(ctx context.Context, tx Tx, runID shared.UUID) error

	// @concept: cascade
	// @decision: non-cascade-direct-to-stale
	CreateNonCascadeStale(ctx context.Context, tx Tx, in NonCascadeStaleInput) (shared.UUID, error)
}

// @concept: node-run
type NodeRunLatest struct {
	RunID              shared.UUID
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
	RunID          shared.UUID
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
	PriorDispatchID             *shared.UUID
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
