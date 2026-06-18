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

type NodeRow struct {
	ID                   shared.UUID       `json:"id"`
	InstanceID           shared.UUID       `json:"instance_id"`
	NodeType             string            `json:"node_type"`
	Executor             string            `json:"executor"`
	State                cascade.NodeState `json:"state"`
	SettlingSignalType   *string           `json:"settling_signal_type,omitempty"`
	CurrentErrorClass    string            `json:"current_error_class,omitempty"`
	RetryCounter         int               `json:"retry_counter"`
	ActionIndex          int               `json:"action_index"`
	AssignedSupervisorID string            `json:"assigned_supervisor_id,omitempty"`
	FrameID              *shared.UUID      `json:"frame_id,omitempty"`
	// @concept: node
	Tags          []string     `json:"tags"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	InFlightRunID *shared.UUID `json:"-"`

	// @concept: run-scope
	RunScopeID *shared.UUID `json:"-"`
}

type NodeCreateInput struct {
	ID         shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string
	Tags       []string
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
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]NodeRow, error)
	CountByState(ctx context.Context, tx Tx) (map[cascade.NodeState]int, error)
	UpdateState(ctx context.Context, id shared.UUID, runScopeID shared.UUID, state cascade.NodeState, reason cascade.TransitionReason, settlingSignalType *string, tx Tx) error
	UpdateError(ctx context.Context, id shared.UUID, es spec.EvaluatorState, tx Tx) error
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	ClearSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error
	ResetFailedTerminalSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error

	// @concept: run-scope
	GetFailedTerminalRunScopeID(ctx context.Context, id shared.UUID, tx Tx) (*shared.UUID, error)

	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
	// @concept: cascade
	MarkStaleForCascade(ctx context.Context, runID shared.UUID, frameID shared.UUID, tx Tx) error

	// @concept: run-scope
	AffirmNodeRunRow(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, frameID shared.UUID, tx Tx) error

	// @concept: signal
	HasRunForNodeInFrame(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (bool, error)

	GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID shared.UUID, tx Tx) (*NodeRunForCallback, error)
}

type NodeRunForCallback struct {
	ID         shared.UUID
	NodeID     shared.UUID
	RunScopeID shared.UUID
	FrameID    shared.UUID
	Phase      string
	State      cascade.NodeState
}
