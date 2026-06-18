// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type RunTreeNode struct {
	RunID shared.UUID
	NodeID shared.UUID
	ParentRunID shared.UUID
	ChildKey string
	FrameID shared.UUID
	State cascade.NodeState
	SettlingSignalType *string
	AggregationPolicy *spec.AggregationPolicy
}

func (r RunTreeNode) IsRoot() bool { return r.ParentRunID == (shared.UUID{}) }

type ChildState struct {
	State              cascade.NodeState
	SettlingSignalType signalpkg.TypePath
	Changed            bool
}

func (c ChildState) IsSettled() bool {
	switch c.State {
	case cascade.NodeStateFresh, cascade.NodeStateFailed, cascade.NodeStateParked:
		return true
	}
	return false
}

func (c ChildState) IsSuccess() bool {
	if c.State != cascade.NodeStateFresh {
		return false
	}
	// @concept: error-policy — `terminal/success` is the canonical
	// fresh-success signal; `terminal/error/*` with state==fresh is a
	// `pass`-colored settle; `pure_cascade` is the scheduler's stale→fresh
	// shortcut.
	if c.SettlingSignalType == "" {
		return true
	}
	if c.SettlingSignalType.HasPrefix("terminal/success") ||
		c.SettlingSignalType.HasPrefix("terminal/error") {
		return true
	}
	return false
}

func (c ChildState) IsFailure() bool {
	return c.State == cascade.NodeStateFailed
}

type AggregateAction int

const (
	AggregateActionNone AggregateAction = iota
	AggregateActionCancelSiblings
	AggregateActionCancelNonWinners
)

type AggregateResult struct {
	IsSettled bool

	ParentState cascade.NodeState

	ParentSettlingSignalType signalpkg.TypePath

	ParentChanged bool

	Action AggregateAction
}

func Aggregate(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	if len(children) == 0 {
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
	return aggregateStrict(children, policy)
}

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
	return AggregateResult{
		IsSettled:                true,
		ParentState:              cascade.NodeStateFresh,
		ParentSettlingSignalType: signalpkg.TypePath("terminal/success"),
		ParentChanged:            aggregateChanged(children),
	}
}

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

func aggregateChanged(children []ChildState) bool {
	for _, c := range children {
		if c.Changed {
			return true
		}
	}
	return false
}

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

func CreateChildRun(
	ctx context.Context, tx persistence.Tx, rt persistence.RunTreeTable, queue persistence.Queue,
	nodeID shared.UUID, frameID shared.UUID, runScopeID shared.UUID,
	executor string, requiredStores []string, policy spec.AggregationPolicy,
) (shared.UUID, error) {
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
