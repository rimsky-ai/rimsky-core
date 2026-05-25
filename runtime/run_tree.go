// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Run-tree types + pure aggregation helpers (E1 + E2 step 2).
//
// Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Run-tree state, a node-run carries an optional parent_run_id linkage
// that forms a tree. Fan-out parents have one child run per partition
// key; sub-graph callers have one child run per non-entry internal
// node. State aggregation walks the tree upward at every terminal,
// recomputing the parent's state per the spec's aggregation rule table.
//
// This file declares the in-memory shape (RunTreeNode / RunTree / ChildState
// / Aggregate) and the pure aggregation function. The persistence wiring
// (read/write parent_run_id, child_key, aggregation_policy on
// rimsky_node_runs; recursive walk under SELECT ... FOR UPDATE) is the
// destructive E2 cutover and lands in a follow-up dispatch.

package runtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
)

// RunTreeNode is the in-memory projection of one rimsky_node_runs row that
// participates in a run-tree. Persistence callers populate it from a
// SELECT; the aggregation function consumes the in-memory shape so it
// can be unit-tested without a database.
type RunTreeNode struct {
	// RunID is the rimsky_node_runs.id (DispatchID).
	RunID shared.UUID
	// NodeID is the rimsky_nodes.id this run is for. The same NodeID
	// may appear in many runs across frames; the RunID is the per-run
	// identity.
	NodeID shared.UUID
	// ParentRunID is the parent in the run-tree, or zero for root runs
	// (top-level outer-graph runs). Persisted on
	// col:rimsky_node_runs.parent_run_id (FK to rimsky_node_runs.id,
	// ON DELETE SET NULL).
	ParentRunID shared.UUID
	// ChildKey is the partition_key (fan-out children) or the
	// internal-node-alias (sub-graph internal children). Empty for
	// root runs. Persisted on col:rimsky_node_runs.child_key.
	ChildKey string
	// FrameID is the frame this run belongs to. Required.
	FrameID shared.UUID
	// State is the run-tree state (mirrors NodeState on rimsky_node_runs).
	State cascade.NodeState
	// SettlingSignalType is the canonical signal type-path the run
	// settled with (concept:signal). Nil for non-settled runs.
	SettlingSignalType *string
	// AggregationPolicy is snapshotted from the template-node spec at
	// run creation. NULL when the run has no children
	// (leaf-style run). Persisted as JSONB on
	// col:rimsky_node_runs.aggregation_policy.
	AggregationPolicy *spec.AggregationPolicy
}

// IsRoot reports whether this run has no parent (top-level run). Root
// runs aggregate up into their containing frame's terminal-state
// computation; non-root runs aggregate up via the run-tree.
func (r RunTreeNode) IsRoot() bool { return r.ParentRunID == (shared.UUID{}) }

// ChildState is the per-child summary the aggregation engine consumes.
// Persistence callers load this from the children rows; the
// aggregation function treats it as opaque (it only cares about state +
// settling signal type-path + changed flag).
//
// SettlingSignalType carries the canonical signal type-path from
// concept:signal: nil for non-settled children, otherwise one of
// "terminal/success", "terminal/error/<class>", "terminal/park/<reason>",
// "terminal/infra/<reason>". The aggregator distinguishes "settled
// success" (state == fresh) from "settled failure" (state == failed)
// via the State field; SettlingSignalType primarily carries the success
// payload's `changed` projection forward to the parent.
//
// Changed projects the success-signal's payload.changed for parents
// that aggregate fresh_changed-vs-fresh_unchanged provenance forward
// (the old `fresh_changed` LastOutcome flavor). Only meaningful when
// State == fresh and SettlingSignalType is "terminal/success".
type ChildState struct {
	State              cascade.NodeState
	SettlingSignalType signalpkg.TypePath
	Changed            bool
}

// IsSettled reports whether the child has reached a settled state
// (fresh, failed, parked). Running / stale are non-settled.
//
// Renamed from IsTerminal in Pass 5 of spec
// 2026-05-23-signal-taxonomy-and-policy-decoupling-design: "terminal"
// in this codebase refers to the wire-protocol StreamClose envelope
// (concept:terminal-resolution); the state-machine landing predicate
// is "settled" — distinct vocabulary.
func (c ChildState) IsSettled() bool {
	switch c.State {
	case cascade.NodeStateFresh, cascade.NodeStateFailed, cascade.NodeStateParked:
		return true
	}
	return false
}

// IsSuccess reports whether the child terminated successfully (settling
// signal-type is terminal/success or this child's pass-color terminal).
// Under the run-tree-aggregator's lens both apply.
func (c ChildState) IsSuccess() bool {
	if c.State != cascade.NodeStateFresh {
		return false
	}
	// terminal/success is the canonical fresh-success signal.
	// terminal/error/* with state == fresh is a `pass`-colored settle
	// (concept:error-policy `pass` action). pure_cascade is the
	// scheduler's stale → fresh shortcut.
	if c.SettlingSignalType == "" {
		// Fresh without a signal-type recorded (pure_cascade /
		// pre-Pass-5 legacy) counts as success.
		return true
	}
	if c.SettlingSignalType.HasPrefix("terminal/success") ||
		c.SettlingSignalType.HasPrefix("terminal/error") {
		return true
	}
	return false
}

// IsFailure reports whether the child terminated in failure.
func (c ChildState) IsFailure() bool {
	return c.State == cascade.NodeStateFailed
}

// AggregateAction is an optional follow-up the aggregation engine asks
// the caller to apply. Used by `strict.cancel_siblings` and `first`
// policies to cancel running / stale siblings.
type AggregateAction int

const (
	// AggregateActionNone — no follow-up.
	AggregateActionNone AggregateAction = iota
	// AggregateActionCancelSiblings — cancel any still-running / stale
	// siblings. The caller walks them in the same transaction.
	AggregateActionCancelSiblings
	// AggregateActionCancelNonWinners — for `first` policy: cancel any
	// child that hasn't reached terminal yet.
	AggregateActionCancelNonWinners
)

// AggregateResult is the return shape of Aggregate. ParentState +
// ParentSettlingSignalType are the new state the parent should adopt
// (or its existing state when nothing settled yet — IsSettled=false
// means the parent stays in its current state). Action carries any
// follow-up cancellation the caller must perform.
type AggregateResult struct {
	// IsSettled indicates whether the aggregation produced a final
	// state for the parent. When false, callers leave the parent in
	// its current state (typically `running`).
	//
	// Renamed from IsTerminal in Pass 5 of spec
	// 2026-05-23-signal-taxonomy-and-policy-decoupling-design — see
	// ChildState.IsSettled docstring for the vocabulary rationale.
	IsSettled bool

	// ParentState is the new state. Only meaningful when IsSettled.
	ParentState cascade.NodeState

	// ParentSettlingSignalType is the canonical signal type-path the
	// parent settles with (concept:signal). The aggregate/<policy>_failed
	// classes (e.g., terminal/error/aggregate/strict_failed) join the
	// canonical taxonomy as new error-class leaves under
	// terminal/error/*. Only meaningful when IsSettled.
	ParentSettlingSignalType signalpkg.TypePath

	// ParentChanged carries the aggregated `changed` projection
	// forward to the parent for `terminal/success` settlements. Mirrors
	// the legacy fresh_changed vs fresh_unchanged distinction.
	ParentChanged bool

	// Action is the optional follow-up. Only set when IsSettled AND
	// the policy demanded a cancel of siblings.
	Action AggregateAction
}

// Aggregate runs the state aggregation rule table per spec
// §State aggregation rules for run-trees. Pure function: takes the
// list of children + the parent's snapshot policy, returns the new
// parent state (when settled) plus any follow-up cancel-action.
//
// Policy.Kind defaults to "strict" when empty.
//
// Decision table:
//
//	strict:           any failure → failed under
//	                  terminal/error/aggregate/strict_failed
//	                  (with strict.cancel_siblings → cancel running siblings)
//	                  all success → success under terminal/success
//	                  (changed aggregated)
//	threshold:        failures < max_failures → success once all settle
//	                  failures ≥ max_failures → failed under
//	                  terminal/error/aggregate/threshold_failed
//	best_effort:      always success once all settle
//	                  (aggregating non-failed outcomes)
//	first:            first success → parent success + cancel non-winners
//	                  all failed   → failed under
//	                  terminal/error/aggregate/first_failed
func Aggregate(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	if len(children) == 0 {
		// No children yet → parent stays in its current state.
		return AggregateResult{IsSettled: false}
	}
	kind := policy.Kind
	if kind == "" {
		kind = "strict"
	}
	switch kind {
	case "strict":
		return aggregateStrict(children, policy)
	case "threshold":
		return aggregateThreshold(children, policy)
	case "best_effort":
		return aggregateBestEffort(children)
	case "first":
		return aggregateFirst(children)
	}
	// Unknown kind: fall back to strict for safety.
	return aggregateStrict(children, policy)
}

// aggregateStrict implements `strict` policy: any failed → failed (with
// optional cancel_siblings); all-settled-and-success → success.
func aggregateStrict(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	anyActive := false
	for _, c := range children {
		if c.IsFailure() {
			res := AggregateResult{
				IsSettled:                true,
				ParentState:              cascade.NodeStateFailed,
				ParentSettlingSignalType: signalpkg.TypePath("terminal/error/aggregate/strict_failed"),
			}
			if policy.CancelSiblings {
				res.Action = AggregateActionCancelSiblings
			}
			return res
		}
		if !c.IsSettled() {
			anyActive = true
		}
	}
	if anyActive {
		return AggregateResult{IsSettled: false}
	}
	// All children settled successfully.
	return AggregateResult{
		IsSettled:                true,
		ParentState:              cascade.NodeStateFresh,
		ParentSettlingSignalType: signalpkg.TypePath("terminal/success"),
		ParentChanged:            aggregateChanged(children),
	}
}

// aggregateThreshold implements `threshold`: failures < max_failures →
// success; failures ≥ max_failures → failed. Only settles when all
// children are settled.
func aggregateThreshold(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	failures := 0
	anyActive := false
	for _, c := range children {
		if c.IsFailure() {
			failures++
		}
		if !c.IsSettled() {
			anyActive = true
		}
	}
	max := policy.MaxFailures
	if max <= 0 {
		// Defensive: threshold without a max is effectively strict.
		max = 1
	}
	if failures >= max {
		return AggregateResult{
			IsSettled:                true,
			ParentState:              cascade.NodeStateFailed,
			ParentSettlingSignalType: signalpkg.TypePath("terminal/error/aggregate/threshold_failed"),
		}
	}
	if anyActive {
		return AggregateResult{IsSettled: false}
	}
	return AggregateResult{
		IsSettled:                true,
		ParentState:              cascade.NodeStateFresh,
		ParentSettlingSignalType: signalpkg.TypePath("terminal/success"),
		ParentChanged:            aggregateChanged(children),
	}
}

// aggregateBestEffort accepts any number of failures; only the
// non-failed children's outcomes contribute. Settles when all children
// are settled.
func aggregateBestEffort(children []ChildState) AggregateResult {
	for _, c := range children {
		if !c.IsSettled() {
			return AggregateResult{IsSettled: false}
		}
	}
	return AggregateResult{
		IsSettled:                true,
		ParentState:              cascade.NodeStateFresh,
		ParentSettlingSignalType: signalpkg.TypePath("terminal/success"),
		ParentChanged:            aggregateChanged(children),
	}
}

// aggregateFirst settles as soon as the first success arrives (cancel
// the rest); failed-only → failed.
func aggregateFirst(children []ChildState) AggregateResult {
	allFailed := true
	for _, c := range children {
		if c.IsSuccess() {
			sig := c.SettlingSignalType
			if sig == "" {
				sig = signalpkg.TypePath("terminal/success")
			}
			return AggregateResult{
				IsSettled:                true,
				ParentState:              cascade.NodeStateFresh,
				ParentSettlingSignalType: sig,
				ParentChanged:            c.Changed,
				Action:                   AggregateActionCancelNonWinners,
			}
		}
		if !c.IsFailure() {
			allFailed = false
		}
	}
	if allFailed {
		return AggregateResult{
			IsSettled:                true,
			ParentState:              cascade.NodeStateFailed,
			ParentSettlingSignalType: signalpkg.TypePath("terminal/error/aggregate/first_failed"),
		}
	}
	return AggregateResult{IsSettled: false}
}

// aggregateChanged returns true when any child's terminal/success
// signal carried changed=true. Per spec §Aggregation rules: the parent
// inherits the "changed" projection forward so a downstream
// `when: payload.changed` subscriber sees a faithful aggregate. Pre-
// Pass-5 this was the fresh_changed-vs-fresh_unchanged distinction;
// now it lives on the signal payload.
func aggregateChanged(children []ChildState) bool {
	for _, c := range children {
		if c.Changed {
			return true
		}
	}
	return false
}

// CreateRootRun is the runtime-side wrapper around
// persistence.RunTreeTable.CreateRootRun. Used by dispatch sites that
// need to create a top-level outer-graph run with a snapshotted
// aggregation policy. The supplied RunScopeID flows through as the
// run's RunScope; for top-level outer-graph dispatches that is the
// instance's main RunScope.
func CreateRootRun(
	ctx context.Context, tx persistence.Tx, rt persistence.RunTreeTable,
	nodeID shared.UUID, frameID shared.UUID, runScopeID shared.UUID,
	executor string, requiredStores []string,
	policy spec.AggregationPolicy,
) (shared.UUID, error) {
	runID := shared.UUID(uuid.New())
	if err := rt.CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
		RunID:             runID,
		NodeID:            nodeID,
		FrameID:           frameID,
		RunScopeID:        runScopeID,
		ExecutorName:      executor,
		RequiredStores:    requiredStores,
		AggregationPolicy: policy,
	}); err != nil {
		return shared.UUID{}, fmt.Errorf("CreateRootRun: %w", err)
	}
	return runID, nil
}

// CreateChildRun is the runtime-side wrapper around
// persistence.RunTreeTable.CreateChildRun. Used by fan-out and
// sub-graph dispatch sites. Idempotent on (node_id, run_scope_id):
// re-creating returns the existing run id reachable via
// Queue.GetInFlightRunForNode.
//
// Under RunScope-first the (parent_run_id, child_key) identity moves
// to the RunScope: the caller allocates the fan-out_partition /
// subgraph RunScope (via RunScopes().Create) BEFORE invoking this
// helper, and threads the resulting RunScope id in. The CreateChildRun
// persistence call is internally idempotent on (node_id, run_scope_id);
// when an in-flight row already exists, the existing run id is reachable
// via queue.GetInFlightRunForNode(nodeID, runScopeID).
func CreateChildRun(
	ctx context.Context, tx persistence.Tx, rt persistence.RunTreeTable, queue persistence.Queue,
	nodeID shared.UUID, frameID shared.UUID, runScopeID shared.UUID,
	executor string, requiredStores []string, policy spec.AggregationPolicy,
) (shared.UUID, error) {
	// Idempotent pre-check: if an in-flight run already exists for this
	// (node, run_scope) pair return its id without inserting.
	if queue != nil {
		existing, ok, err := queue.GetInFlightRunForNode(ctx, tx, nodeID, runScopeID)
		if err != nil {
			return shared.UUID{}, fmt.Errorf("CreateChildRun: lookup in-flight: %w", err)
		}
		if ok {
			return existing, nil
		}
	}
	runID := shared.UUID(uuid.New())
	if err := rt.CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
		RunID:             runID,
		NodeID:            nodeID,
		FrameID:           frameID,
		RunScopeID:        runScopeID,
		ExecutorName:      executor,
		RequiredStores:    requiredStores,
		AggregationPolicy: policy,
	}); err != nil {
		return shared.UUID{}, fmt.Errorf("CreateChildRun: %w", err)
	}
	return runID, nil
}

// GetRunTree returns the run-tree rooted at runID (the row plus its
// transitive descendants). Used by the control-api observability layer
// to project a run's full tree. Bounded by the practical depth of the
// fan-out / sub-graph composition.
//
// Returns ([]persistence.RunTreeRow, error). The first element is the
// root row; subsequent elements are children in BFS order.
func GetRunTree(
	ctx context.Context, tx persistence.Tx, rt persistence.RunTreeTable,
	runID shared.UUID,
) ([]persistence.RunTreeRow, error) {
	root, err := rt.GetByID(ctx, tx, runID)
	if err != nil {
		return nil, fmt.Errorf("GetRunTree: load root: %w", err)
	}
	if root == nil {
		return nil, nil
	}
	out := []persistence.RunTreeRow{*root}
	queue := []shared.UUID{runID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := rt.ListChildren(ctx, tx, current)
		if err != nil {
			return nil, fmt.Errorf("GetRunTree: list children of %s: %w", current, err)
		}
		for _, c := range children {
			out = append(out, c)
			queue = append(queue, c.RunID)
		}
	}
	return out, nil
}
