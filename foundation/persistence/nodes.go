package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// NodeRow mirrors a row of rimsky_nodes — the per-instance node-state
// row driven by the cascade engine and the supervisor.
type NodeRow struct {
	ID                   shared.UUID      `json:"id"`
	InstanceID           shared.UUID      `json:"instance_id"`
	NodeType             string           `json:"node_type"`
	Executor             string           `json:"executor"`
	ScheduleCron         string           `json:"schedule_cron"`
	State                shared.NodeState `json:"state"`
	Dependencies         []shared.UUID    `json:"dependencies"`
	CurrentErrorClass    string           `json:"current_error_class,omitempty"`
	RetryCounter         int              `json:"retry_counter"`
	ActionIndex          int              `json:"action_index"`
	LastHeartbeatAt      *time.Time       `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string           `json:"assigned_supervisor_id,omitempty"`
	FrameID              *shared.UUID     `json:"frame_id,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

// NodeCreateInput is the per-row input for Create.
type NodeCreateInput struct {
	ID           shared.UUID
	InstanceID   shared.UUID
	NodeType     string
	Executor     string
	ScheduleCron string
	Dependencies []shared.UUID
}

// NodeStore is the rimsky_nodes accessor.
type NodeStore interface {
	Create(ctx context.Context, in NodeCreateInput, tx Tx) (NodeRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*NodeRow, error)
	ListByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]NodeRow, error)
	ListByInstancePaged(ctx context.Context, instanceID shared.UUID, pag ListPagination, tx Tx) (PaginatedListResult[NodeRow], error)
	ListReadyForDispatch(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListRunning(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListDependentsOf(ctx context.Context, nodeID shared.UUID, tx Tx) ([]NodeRow, error)
	ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx Tx) ([]NodeRow, error)
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]NodeRow, error)
	CountByState(ctx context.Context, tx Tx) (map[shared.NodeState]int, error)
	UpdateState(ctx context.Context, id shared.UUID, state shared.NodeState, reason cascade.TransitionReason, tx Tx) error
	UpdateError(ctx context.Context, id shared.UUID, es node.EvaluatorState, tx Tx) error
	UpdateHeartbeat(ctx context.Context, id shared.UUID, at time.Time, supervisorID string, tx Tx) error
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx Tx) error
	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
	// MarkStaleForCascade is the cascade-from-parent-commit helper used by
	// the supervisor's terminal-complete path. Sets state='stale',
	// frame_id=$1 only for rows currently fresh OR (stale AND frame_id IS
	// NULL). Used in lieu of UpdateState because the cascade target needs
	// the frame_id atomically and the predicate is gated.
	MarkStaleForCascade(ctx context.Context, id shared.UUID, frameID shared.UUID, tx Tx) error
}
