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

// NodeRow mirrors a row of rimsky_nodes — the per-instance node-state
// row driven by the cascade engine and the supervisor.
//
// Post-2026-05-14: the `dependencies` column retired in favour of the
// receiver-side `subscribes:` model. Cascade-coupled receivers are
// resolved via the per-template subscription-edge inverse map (see
// runtime/subscription_loaders.go::resolveSubscribedSenders); the
// retired column is no longer surfaced on the row type.
type NodeRow struct {
	ID         shared.UUID       `json:"id"`
	InstanceID shared.UUID       `json:"instance_id"`
	NodeType   string            `json:"node_type"`
	Executor   string            `json:"executor"`
	State      cascade.NodeState `json:"state"`
	// SettlingSignalType carries the canonical signal type-path
	// (concept:signal) of the run's settling resolution
	// (terminal/success, terminal/error/<class>, terminal/park/<reason>,
	// terminal/infra/<reason>). Nil-pointer while the run is in-flight.
	// Replaces the retired LastOutcome enum post-Pass 5 of spec
	// 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
	SettlingSignalType   *string      `json:"settling_signal_type,omitempty"`
	CurrentErrorClass    string       `json:"current_error_class,omitempty"`
	RetryCounter         int          `json:"retry_counter"`
	ActionIndex          int          `json:"action_index"`
	AssignedSupervisorID string       `json:"assigned_supervisor_id,omitempty"`
	FrameID              *shared.UUID `json:"frame_id,omitempty"`
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
	// InFlightRunID is the rimsky_node_runs.id of the in-flight run row
	// projected by the LATERAL/JOIN in `nodeSelect` (when one exists).
	// Nil when the node has no in-flight run (state implicitly 'fresh'
	// or last-terminal-failed). Conductor / scheduler paths that drive
	// `UpdateState` from a NodeRow read use this as the run-id
	// disambiguator so the state-machine update lands on the projected
	// row rather than an arbitrary fan-out sibling sharing node_id.
	// Not serialized — internal-only, used by foundation/runtime code
	// to thread the per-row identity through transitions.
	InFlightRunID *shared.UUID `json:"-"`

	// RunScopeID is the RunScope id of the node's current in-flight
	// run (projected via the same LATERAL/CASE that produces
	// InFlightRunID). Nil when no in-flight run exists.
	//
	// @concept: run-scope
	RunScopeID *shared.UUID `json:"-"`
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
	// @deliberate: ListByInstancePagedFiltered is the filter-aware
	// variant; pass a zero-valued NodeListFilter for the same behaviour
	// as ListByInstancePaged. The two methods coexist so existing
	// callers stay unaffected.
	ListByInstancePagedFiltered(ctx context.Context, instanceID shared.UUID, pag ListPagination, filter NodeListFilter, tx Tx) (PaginatedListResult[NodeRow], error)
	ListReadyForDispatch(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListRunning(ctx context.Context, tx Tx) ([]NodeRow, error)
	ListPureCascadeReady(ctx context.Context, tx Tx) ([]NodeRow, error)
	CountByState(ctx context.Context, tx Tx) (map[cascade.NodeState]int, error)
	// @agent-contract UpdateState transitions the node to `state` under
	// `reason`, validated against the cascade state machine.
	// `settlingSignalType` is the canonical signal type-path
	// (concept:signal) recorded on settling transitions; nil means "do
	// not write the column" (preserves the existing value — used for
	// non-settling transitions like stale→running). Settling
	// transitions pass a non-nil pointer holding one of
	// "terminal/success" | "terminal/error/<class>" |
	// "terminal/park/<reason>" | "terminal/infra/<reason>".
	//
	// `runScopeID` disambiguates which in-flight rimsky_node_runs row to
	// transition. Fan-out children share a node_id with the parent and
	// each sibling (per `runtime/fanout_dispatch.go::dispatchFanOutChildren`
	// and the split UNIQUE constraints in
	// `foundation/persistence/postgres/migrations/001-baseline.sql`),
	// so a SELECT by node_id alone can return any of the in-flight rows
	// while children race. Callers in the dispatch path
	// (transitionToRunning, terminal handlers, error policy, parked
	// sweep, parked wake) MUST pass the specific run's RunScope — so the
	// state-machine update lands on the intended row. See spec
	// .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
	// for the bug that motivated this disambiguation.
	UpdateState(ctx context.Context, id shared.UUID, runScopeID shared.UUID, state cascade.NodeState, reason cascade.TransitionReason, settlingSignalType *string, tx Tx) error
	UpdateError(ctx context.Context, id shared.UUID, es spec.EvaluatorState, tx Tx) error
	SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx Tx) error
	// @agent-contract ClearSettlingSignalType clears
	// settling_signal_type to NULL on the in-flight rimsky_node_runs
	// row. Used by the operator reset path so the dashboard does not
	// display a stale failed signal-type-path while the node
	// transitions back through stale → running → fresh. No-op when no
	// in-flight row exists.
	//
	// `runScopeID` narrows the UPDATE to the specific in-flight row —
	// required for fan-out children that share a node_id with siblings,
	// to prevent the clear from landing on an arbitrary sibling.
	//
	// Replaces the retired ClearLastOutcome alongside Pass 5 of spec
	// 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
	ClearSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error
	// @agent-contract ResetFailedTerminalSettlingSignalType clears
	// settling_signal_type to NULL on the most-recent failed-terminal
	// `rimsky_node_runs` row for the given node (predicate `phase =
	// 'failed'`, ordered by `active_terminal_at DESC LIMIT 1`). Used by
	// the operator reset path: handleResetNode is invoked when the node
	// is in state='failed', which means the only state-bearing row is
	// the failed-terminal row (no in-flight row exists);
	// ClearSettlingSignalType's `phase IN (pending,active,held,parked)`
	// predicate would therefore be a no-op.
	//
	// No-op when no failed-terminal row exists.
	//
	// Replaces the retired ResetFailedTerminalLastOutcome alongside
	// Pass 5 of spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
	ResetFailedTerminalSettlingSignalType(ctx context.Context, id shared.UUID, runScopeID shared.UUID, tx Tx) error

	// @agent-contract GetFailedTerminalRunScopeID returns the
	// run_scope_id of the most-recent failed-terminal
	// `rimsky_node_runs` row for the node (predicate `phase =
	// 'failed'`, ordered by `COALESCE(active_terminal_at, enqueued_at)
	// DESC LIMIT 1`). Returns nil when no failed-terminal row exists.
	//
	// Used by the operator reset path so the failed-terminal
	// settling_signal_type reset can key on the correct RunScope
	// without requiring the caller to know it in advance —
	// NodeRow.RunScopeID surfaces only the in-flight scope and is
	// nil for a failed node.
	//
	// @concept: run-scope
	GetFailedTerminalRunScopeID(ctx context.Context, id shared.UUID, tx Tx) (*shared.UUID, error)

	DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx Tx) error
	// @agent-contract MarkStaleForCascade transitions the run's state
	// to 'stale' and pins frame_id. Pure UPDATE keyed by run_id;
	// allocation is the cascade walker's responsibility via
	// AffirmNodeRunRow.
	//
	// @blessed-invariant: state-machine-writes-single-tx — State-machine writes for a single run must be
	// tx-atomic. Caller MUST resolve the run id (via the affirm-then-read
	// pattern) within the same tx as this UPDATE.
	//
	// @concept: cascade
	MarkStaleForCascade(ctx context.Context, runID shared.UUID, frameID shared.UUID, tx Tx) error

	// @agent-contract AffirmNodeRunRow ensures an in-flight
	// rimsky_node_runs row exists for (nodeID, runScopeID). If no
	// in-flight row exists, INSERTs a pending stale row keyed to the
	// supplied frameID; if one exists, no-op. Returns only error.
	//
	// Callers MUST NOT depend on this method's return shape beyond
	// error/no-error. The architectural property: lazy↔eager
	// allocation is a no-op rewrite (every AffirmNodeRunRow call could
	// be deleted with no other code change if the system switched to
	// eager allocation at RunScope creation time).
	//
	// Errors:
	//   - ErrRunScopeClosed: the RunScope's closed_at is set.
	//   - underlying database errors: propagated.
	//
	// @blessed-invariant: affirm-node-run-row — AffirmNodeRunRow no-return-value-dependency
	// per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
	//
	// @concept: run-scope
	AffirmNodeRunRow(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, frameID shared.UUID, tx Tx) error

	// @agent-contract HasRunForNodeInFrame reports whether ANY
	// rimsky_node_runs row (regardless of phase) exists for `nodeID`
	// with `frame_id = frameID`. Used by the cascade walker's
	// once-per-frame guard so a receiver that has already dispatched +
	// terminated within a frame is not re-affirmed by a later sender's
	// cascade in the same frame.
	//
	// @concept: signal
	HasRunForNodeInFrame(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx Tx) (bool, error)

	// @agent-contract GetRunByDispatchIDForUpdate returns the in-flight
	// rimsky_node_runs row projection for the given dispatch_id, with
	// SELECT ... FOR UPDATE row lock. Returns nil if no in-flight row
	// exists for that id. Used by the callback handler in
	// runtime/callback.go to resolve the run for a callback under the
	// atomic phase check.
	//
	// @blessed-invariant: callback-determinism — Callback determinism per spec.
	GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID shared.UUID, tx Tx) (*NodeRunForCallback, error)
}

// NodeRunForCallback is the projection returned by
// GetRunByDispatchIDForUpdate for the callback path. Carries the
// fields the callback handler needs without dragging in the full
// DispatchRow shape.
type NodeRunForCallback struct {
	ID         shared.UUID
	NodeID     shared.UUID
	RunScopeID shared.UUID
	FrameID    shared.UUID
	Phase      string
	State      cascade.NodeState
}
