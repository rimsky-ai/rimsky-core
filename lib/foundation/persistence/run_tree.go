// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_tree.go is the persistence accessor for the run-tree extension to
// `rimsky_node_runs` (spec §Run-tree and aggregation). Tracks
// parent/child run linkage, snapshotted aggregation policy, and the
// state/last_outcome columns lifted from `rimsky_nodes`.
//
// @concept: run-scope
// @concept: terminal-resolution
//
// The leaf-style state (one row per node per frame, no children) was
// previously read/written via `NodeTable.UpdateState` against
// `rimsky_nodes`. The run-tree extension lifts those columns onto
// `rimsky_node_runs.state` / `last_outcome` so that fan-out and
// sub-graph composition can carry per-run state independently of the
// node row. Pre-v1 break-freely (per `.claude/rules/rules.md`): once
// every caller migrates to the run-tree API, the `rimsky_nodes` state
// columns drop. Until then, the run-tree state column is additive and
// the two paths coexist.

package persistence

import (
	"context"
	"encoding/json"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// RunTreeRow is the run-tree projection of one `rimsky_node_runs` row.
// Persistence callers populate it from a SELECT; the runtime aggregation
// engine (`runtime/run_tree.go::Aggregate`) consumes the in-memory shape
// so it can be unit-tested without a database.
type RunTreeRow struct {
	RunID      shared.UUID `json:"run_id"`
	NodeID     shared.UUID `json:"node_id"`
	FrameID    shared.UUID `json:"frame_id"`
	RunScopeID shared.UUID `json:"run_scope_id"`
	// Phase carries the rimsky_node_runs.phase value so callers don't
	// need a separate dispatch-row fetch. Optional projection: empty
	// string when the implementation did not project it.
	Phase string            `json:"phase,omitempty"`
	State cascade.NodeState `json:"state"`
	// SettlingSignalType carries the canonical signal type-path
	// (concept:signal) of the run's settling resolution
	// (terminal/success, terminal/error/<class>, terminal/park/<reason>,
	// terminal/infra/<reason>). Nil-pointer while the run is in-flight.
	// Replaces the retired LastOutcome enum post-Pass 5 of spec
	// 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
	SettlingSignalType *string `json:"settling_signal_type,omitempty"`
	// AggregationPolicy is the parent's snapshot policy. Leaf runs have
	// no policy (zero value). Persisted as JSONB on
	// `col:rimsky_node_runs.aggregation_policy`.
	AggregationPolicy spec.AggregationPolicy `json:"aggregation_policy,omitempty"`
}

// CreateRootRunInput is the payload for RunTreeTable.CreateRootRun.
type CreateRootRunInput struct {
	RunID             shared.UUID
	NodeID            shared.UUID
	FrameID           shared.UUID
	RunScopeID        shared.UUID
	AggregationPolicy spec.AggregationPolicy
	// ExecutorName + RequiredStores are forwarded to the underlying
	// dispatch row insert so callers do not have to round-trip through
	// `Queue.EnqueueInTx`. Empty ExecutorName is legal for orchestration-
	// only parent runs (sub-graph callers absorb the entry's executor at
	// canonicalization; the runtime caller resolves it then).
	ExecutorName   string
	RequiredStores []string
}

// CreateChildRunInput is the payload for RunTreeTable.CreateChildRun.
//
// Idempotency: re-creating a child with the same `(node_id,
// run_scope_id)` returns nil without error. The existing run is
// reachable via Queue.GetInFlightRunForNode(node_id, run_scope_id).
type CreateChildRunInput struct {
	RunID          shared.UUID
	NodeID         shared.UUID
	FrameID        shared.UUID
	RunScopeID     shared.UUID
	ExecutorName   string
	RequiredStores []string
	// AggregationPolicy is set when the child is itself a parent (nested
	// fan-out, sub-graph composition). Empty otherwise.
	AggregationPolicy spec.AggregationPolicy
}

// RunTreeTable is the run-tree accessor on `rimsky_node_runs`. All
// methods take an explicit tx; callers wrap them in
// `Tables.Transaction` for atomicity across multiple state writes.
//
// @agent-contract:
//
//	what:        run-tree CRUD on rimsky_node_runs (state, last_outcome,
//	             parent_run_id, child_key, aggregation_policy).
//	how to use:  build atomic state-propagation via LockTreeForUpdate +
//	             ListChildren + UpdateStateAndOutcome inside a single tx.
//	handles:     idempotent child creation; parent-row locking; state
//	             reads/writes.
//	does NOT:    state-machine validation (callers consult
//	             `foundation/cascade/state.go::NextStateParent`);
//	             aggregation rule application (callers consult
//	             `runtime/run_tree.go::Aggregate`); claim-handle resolution
//	             (callers invoke `runtime/auto_terminal.go`).
//	threadsafe:  serializable per the caller's tx isolation; ancestor
//	             locks taken in tree order (upward) avoid deadlock.
type RunTreeTable interface {
	// CreateRootRun inserts a top-level run (parent_run_id NULL,
	// child_key NULL). Stale-marked by default (the existing dispatch
	// row insert is what `Queue.EnqueueInTx` does today; this is the
	// run-tree-aware variant that ALSO carries the aggregation_policy).
	CreateRootRun(ctx context.Context, tx Tx, in CreateRootRunInput) error

	// CreateChildRun inserts a child run within the given RunScope.
	// Idempotent on (node_id, run_scope_id): re-creates return nil
	// without error. Existing run is reachable via
	// Queue.GetInFlightRunForNode(node_id, run_scope_id).
	CreateChildRun(ctx context.Context, tx Tx, in CreateChildRunInput) error

	// GetByID returns the run-tree row for a given run id, or nil when
	// the row does not exist.
	GetByID(ctx context.Context, tx Tx, runID shared.UUID) (*RunTreeRow, error)

	// LockTreeForUpdate runs SELECT ... FOR UPDATE on the run row
	// identified by runID. Used by the state-propagation transaction
	// before reading children + writing the parent state.
	LockTreeForUpdate(ctx context.Context, tx Tx, runID shared.UUID) (*RunTreeRow, error)

	// ListChildren returns all in-flight run rows in RunScopes whose
	// parent_run_id equals parentRunID. Walks via rimsky_run_scopes JOIN.
	// Used to evaluate aggregation rules over the parent's children.
	ListChildren(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunTreeRow, error)

	// UpdateStateAndOutcome writes a new (state, settling_signal_type)
	// pair on the run row. settlingSignalType nil means "do not write
	// the column" (preserves existing value — used for non-settling
	// transitions). Settling transitions pass a non-nil pointer holding
	// the canonical signal type-path per concept:signal. Does NOT
	// validate the transition — callers consult cascade.NextState /
	// cascade.NextStateParent before invoking.
	UpdateStateAndOutcome(ctx context.Context, tx Tx, runID shared.UUID, state cascade.NodeState, settlingSignalType *string) error

	// UpdateAggregationPolicy snapshots a new aggregation policy onto
	// the run row. Used when canonicalization-time policy is overridden
	// (rare).
	UpdateAggregationPolicy(ctx context.Context, tx Tx, runID shared.UUID, policy spec.AggregationPolicy) error
}

// MarshalAggregationPolicy is the canonical JSON encoding for the
// aggregation_policy JSONB / TEXT column. Exposed so test fixtures and
// migration helpers can produce byte-equal payloads. Returns nil for
// the zero-value policy so the column stores NULL.
func MarshalAggregationPolicy(p spec.AggregationPolicy) ([]byte, error) {
	if p.Kind == "" && !p.CancelSiblings && p.MaxFailures == 0 {
		return nil, nil
	}
	return json.Marshal(p)
}

// UnmarshalAggregationPolicy is the inverse helper. Empty / NULL bytes
// produce a zero-value policy.
func UnmarshalAggregationPolicy(b []byte) (spec.AggregationPolicy, error) {
	var p spec.AggregationPolicy
	if len(b) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	return p, nil
}
