// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// NodeRow mirrors a row of rimsky_nodes — the per-instance node-state
// row driven by the cascade engine and the supervisor.
//
// Post-2026-05-14: the `dependencies` column retired in favour of the
// receiver-side `subscribes:` model. Cascade-coupled receivers are
// resolved via the per-template subscription-edge inverse map (see
// runtime/subscription_loaders.go::resolveSubscribedSenders); the
// retired column is no longer surfaced on the row type.
type NodeRow struct {
	ID                   shared.UUID         `json:"id"`
	InstanceID           shared.UUID         `json:"instance_id"`
	NodeType             string              `json:"node_type"`
	Executor             string              `json:"executor"`
	State                cascade.NodeState   `json:"state"`
	LastOutcome          cascade.LastOutcome `json:"last_outcome,omitempty"`
	CurrentErrorClass    string              `json:"current_error_class,omitempty"`
	RetryCounter         int                 `json:"retry_counter"`
	ActionIndex          int                 `json:"action_index"`
	LastHeartbeatAt      *time.Time          `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string              `json:"assigned_supervisor_id,omitempty"`
	FrameID              *shared.UUID        `json:"frame_id,omitempty"`
	// Tags is operator-facing metadata projected from the bound
	// template's `TemplateNodeDef.Tags` at instance creation, after
	// materialization-time `{{params.<key>}}` substitution. Per spec
	// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
	// Item 4. Always emitted (no `omitempty`): empty array means "no
	// tags", not "unknown".
	//
	// @concept: node
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NodeCreateInput is the per-row input for Create.
type NodeCreateInput struct {
	ID         shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string
	// Tags is the resolved (post-substitution) tag list to persist.
	// The instance-create handler runs params-only substitution at
	// materialization time; an empty slice is fine.
	Tags []string
}

// NodeListFilter narrows ListByInstancePaged returns. Empty fields mean
// "no filter". Currently a single-value tag exact-match (per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 4 — multi-tag combinations are deferred).
type NodeListFilter struct {
	Tag string
}

// NodeTable is the rimsky_nodes accessor.
type NodeTable interface {
	Create(ctx context.Context, in NodeCreateInput, tx Tx) (NodeRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*NodeRow, error)
	ListByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]NodeRow, error)
	ListByInstancePaged(ctx context.Context, instanceID shared.UUID, pag ListPagination, tx Tx) (PaginatedListResult[NodeRow], error)
	// ListByInstancePagedFiltered is the filter-aware variant; pass a
	// zero-valued NodeListFilter for the same behaviour as
	// ListByInstancePaged. The two methods coexist so existing callers
	// stay unaffected.
	ListByInstancePagedFiltered(ctx context.Context, instanceID shared.UUID, pag ListPagination, filter NodeListFilter, tx Tx) (PaginatedListResult[NodeRow], error)
	ListReadyForDispatch(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListRunning(ctx context.Context, tx Tx) ([]NodeRow, error)
	// ListRunningBySupervisor returns the rows in state='running' currently
	// assigned to the given supervisor (`assigned_supervisor_id = $1`).
	// Used by the supervisor's heartbeat tick to refresh `last_heartbeat_at`
	// on every running node it owns — covers both sync dispatches (RunNode
	// in-flight) and async dispatches (handed off to the callback server
	// but still running in the DB until the terminal callback arrives).
	// The DB is the source of truth; do not rely on in-memory bookkeeping
	// of "currently running" nodes.
	ListRunningBySupervisor(ctx context.Context, supervisorID string, tx Tx) ([]NodeRow, error)
	ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx Tx) ([]NodeRow, error)
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]NodeRow, error)
	CountByState(ctx context.Context, tx Tx) (map[cascade.NodeState]int, error)
	// UpdateState transitions the node to `state` under `reason`, validated
	// against the cascade state machine. `lastOutcome` is the resolution
	// flavor for terminal-for-this-frame transitions; the empty string
	// "" means "do not write the column" (preserves the existing value).
	// See graph/shared/types.go for LastOutcome values.
	UpdateState(ctx context.Context, id shared.UUID, state cascade.NodeState, reason cascade.TransitionReason, lastOutcome cascade.LastOutcome, tx Tx) error
	UpdateError(ctx context.Context, id shared.UUID, es spec.EvaluatorState, tx Tx) error
	UpdateHeartbeat(ctx context.Context, id shared.UUID, at time.Time, supervisorID string, tx Tx) error
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	// ClearLastOutcome sets last_outcome = NULL. Used by the operator
	// reset path so the dashboard does not display a stale `failed`
	// resolution flavor while the node transitions back through
	// stale → running → fresh.
	ClearLastOutcome(ctx context.Context, id shared.UUID, tx Tx) error
	ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx Tx) error
	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
	// MarkStaleForCascade is the cascade-from-parent-commit helper used by
	// the supervisor's terminal-complete path. Sets state='stale',
	// frame_id=$1 only for rows currently fresh OR (stale AND frame_id IS
	// NULL). Used in lieu of UpdateState because the cascade target needs
	// the frame_id atomically and the predicate is gated.
	//
	// Returns `inserted=true` when this call actually inserted a new
	// pending run row (the row was eligible — either no in-flight run
	// existed, or the existing run already matched the requested frame
	// and the INSERT was a no-op under WHERE NOT EXISTS). The caller can
	// use this signal to skip duplicate state-transition audit events on
	// diamond hard-dep topologies (see
	// `runtime/cascade_invalidate.go::stalemarkAndEnqueueInFrame`).
	MarkStaleForCascade(ctx context.Context, id shared.UUID, frameID shared.UUID, tx Tx) (inserted bool, err error)
}
