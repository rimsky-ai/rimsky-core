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

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
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
	// State is the run-tree state (mirrors NodeState / LastOutcome on
	// rimsky_nodes today; the state-propagation cutover in E2 moves
	// these onto rimsky_node_runs.state + last_outcome).
	State       cascade.NodeState
	LastOutcome cascade.LastOutcome
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
// last_outcome).
type ChildState struct {
	State       cascade.NodeState
	LastOutcome cascade.LastOutcome
}

// IsTerminal reports whether the child has reached a settled state
// (fresh, failed, parked). Running / stale are non-terminal.
func (c ChildState) IsTerminal() bool {
	switch c.State {
	case cascade.NodeStateFresh, cascade.NodeStateFailed, cascade.NodeStateParked:
		return true
	}
	return false
}

// IsSuccess reports whether the child terminated successfully (any of
// the fresh_* / passed / pure_cascade outcomes).
func (c ChildState) IsSuccess() bool {
	if c.State != cascade.NodeStateFresh {
		return false
	}
	switch c.LastOutcome {
	case cascade.LastOutcomeFreshChanged, cascade.LastOutcomeFreshUnchanged,
		cascade.LastOutcomePassed, cascade.LastOutcomePureCascade:
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
// ParentOutcome are the new state the parent should adopt (or its
// existing state when nothing settled yet — IsTerminal=false means
// the parent stays in its current state). Action carries any
// follow-up cancellation the caller must perform.
type AggregateResult struct {
	// IsTerminal indicates whether the aggregation produced a final
	// state for the parent. When false, callers leave the parent in
	// its current state (typically `running`).
	IsTerminal bool

	// ParentState is the new state. Only meaningful when IsTerminal.
	ParentState cascade.NodeState

	// ParentOutcome is the new last_outcome. Only meaningful when
	// IsTerminal.
	ParentOutcome cascade.LastOutcome

	// Action is the optional follow-up. Only set when IsTerminal AND
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
//	strict:           any failure → failed (with strict.cancel_siblings → cancel running siblings)
//	                  all success → success (last_outcome aggregated)
//	threshold:        failures < max_failures → success once all settle
//	                  failures ≥ max_failures → failed
//	best_effort:      always success once all settle (aggregating non-failed outcomes)
//	first:            first success → parent success + cancel non-winners
//	                  all failed   → failed
func Aggregate(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	if len(children) == 0 {
		// No children yet → parent stays in its current state.
		return AggregateResult{IsTerminal: false}
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
				IsTerminal:    true,
				ParentState:   cascade.NodeStateFailed,
				ParentOutcome: cascade.LastOutcomeFailed,
			}
			if policy.CancelSiblings {
				res.Action = AggregateActionCancelSiblings
			}
			return res
		}
		if !c.IsTerminal() {
			anyActive = true
		}
	}
	if anyActive {
		return AggregateResult{IsTerminal: false}
	}
	// All children settled successfully.
	return AggregateResult{
		IsTerminal:    true,
		ParentState:   cascade.NodeStateFresh,
		ParentOutcome: aggregateSuccessOutcome(children),
	}
}

// aggregateThreshold implements `threshold`: failures < max_failures →
// success; failures ≥ max_failures → failed. Only settles when all
// children are terminal.
func aggregateThreshold(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	failures := 0
	anyActive := false
	for _, c := range children {
		if c.IsFailure() {
			failures++
		}
		if !c.IsTerminal() {
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
			IsTerminal:    true,
			ParentState:   cascade.NodeStateFailed,
			ParentOutcome: cascade.LastOutcomeFailed,
		}
	}
	if anyActive {
		return AggregateResult{IsTerminal: false}
	}
	return AggregateResult{
		IsTerminal:    true,
		ParentState:   cascade.NodeStateFresh,
		ParentOutcome: aggregateSuccessOutcome(children),
	}
}

// aggregateBestEffort accepts any number of failures; only the
// non-failed children's outcomes contribute. Settles when all children
// are terminal.
func aggregateBestEffort(children []ChildState) AggregateResult {
	for _, c := range children {
		if !c.IsTerminal() {
			return AggregateResult{IsTerminal: false}
		}
	}
	return AggregateResult{
		IsTerminal:    true,
		ParentState:   cascade.NodeStateFresh,
		ParentOutcome: aggregateSuccessOutcome(children),
	}
}

// aggregateFirst settles as soon as the first success arrives (cancel
// the rest); failed-only → failed.
func aggregateFirst(children []ChildState) AggregateResult {
	allFailed := true
	for _, c := range children {
		if c.IsSuccess() {
			return AggregateResult{
				IsTerminal:    true,
				ParentState:   cascade.NodeStateFresh,
				ParentOutcome: c.LastOutcome,
				Action:        AggregateActionCancelNonWinners,
			}
		}
		if !c.IsFailure() {
			allFailed = false
		}
	}
	if allFailed {
		return AggregateResult{
			IsTerminal:    true,
			ParentState:   cascade.NodeStateFailed,
			ParentOutcome: cascade.LastOutcomeFailed,
		}
	}
	return AggregateResult{IsTerminal: false}
}

// aggregateSuccessOutcome returns the last_outcome the parent adopts
// when all children settled successfully. Per spec §Aggregation rules:
// if any child reported fresh_changed, the parent reports fresh_changed
// (the cascade-firing gate). Otherwise fresh_unchanged.
func aggregateSuccessOutcome(children []ChildState) cascade.LastOutcome {
	for _, c := range children {
		if c.LastOutcome == cascade.LastOutcomeFreshChanged {
			return cascade.LastOutcomeFreshChanged
		}
	}
	return cascade.LastOutcomeFreshUnchanged
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
