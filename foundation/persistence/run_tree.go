// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run_tree.go is the persistence accessor for the run-tree extension to
// `rimsky_node_runs` (spec §Run-tree and aggregation). Tracks
// parent/child run linkage, snapshotted aggregation policy, and the
// state/last_outcome columns lifted from `rimsky_nodes`.
//
// @concept: run-tree
// @concept: aggregation-policy
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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// RunTreeRow is the run-tree projection of one `rimsky_node_runs` row.
// Persistence callers populate it from a SELECT; the runtime aggregation
// engine (`runtime/run_tree.go::Aggregate`) consumes the in-memory shape
// so it can be unit-tested without a database.
type RunTreeRow struct {
	RunID       shared.UUID         `json:"run_id"`
	NodeID      shared.UUID         `json:"node_id"`
	FrameID     shared.UUID         `json:"frame_id"`
	ParentRunID *shared.UUID        `json:"parent_run_id,omitempty"`
	ChildKey    string              `json:"child_key,omitempty"`
	State       cascade.NodeState   `json:"state"`
	LastOutcome cascade.LastOutcome `json:"last_outcome,omitempty"`
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
// Idempotency: re-creating a child with the same `(parent_run_id,
// child_key)` returns the existing row's run id. Callers ignore the
// returned id and re-load via GetByID when they need the row.
type CreateChildRunInput struct {
	RunID          shared.UUID
	NodeID         shared.UUID
	FrameID        shared.UUID
	ParentRunID    shared.UUID
	ChildKey       string
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

	// CreateChildRun inserts a child run for the given parent +
	// child_key. Idempotent on (parent_run_id, child_key): re-creates
	// return nil without error and the existing row id is reachable via
	// GetByParentChildKey.
	CreateChildRun(ctx context.Context, tx Tx, in CreateChildRunInput) error

	// GetByID returns the run-tree row for a given run id, or nil when
	// the row does not exist.
	GetByID(ctx context.Context, tx Tx, runID shared.UUID) (*RunTreeRow, error)

	// GetByParentChildKey returns the existing run for a (parent,
	// child_key) pair. Used by callers that want the idempotency
	// guarantee of CreateChildRun.
	GetByParentChildKey(ctx context.Context, tx Tx, parentRunID shared.UUID, childKey string) (*RunTreeRow, error)

	// LockTreeForUpdate runs SELECT ... FOR UPDATE on the run row
	// identified by runID. Used by the state-propagation transaction
	// before reading children + writing the parent state.
	LockTreeForUpdate(ctx context.Context, tx Tx, runID shared.UUID) (*RunTreeRow, error)

	// ListChildren returns all rows whose parent_run_id equals
	// parentRunID. Used to evaluate aggregation rules over the parent's
	// children.
	ListChildren(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunTreeRow, error)

	// UpdateStateAndOutcome writes a new (state, last_outcome) pair on
	// the run row. lastOutcome == "" means "do not write the column"
	// (preserves existing value). Does NOT validate the transition —
	// callers consult cascade.NextState / cascade.NextStateParent before
	// invoking.
	UpdateStateAndOutcome(ctx context.Context, tx Tx, runID shared.UUID, state cascade.NodeState, lastOutcome cascade.LastOutcome) error

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
