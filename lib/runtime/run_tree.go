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

type ChildState struct {
	State              cascade.NodeState
	SettlingSignalType *signalpkg.TypePath
	Changed            bool
}

func (c ChildState) IsSettled() bool {
	switch c.State {
	case cascade.NodeStateFresh, cascade.NodeStateFailed:
		return true
	}
	return false
}

// @concept: error-policy
func (c ChildState) IsSuccess() bool {
	if c.State != cascade.NodeStateFresh {
		return false
	}
	if c.SettlingSignalType == nil {
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

func recordRunTreeChanged(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	runID shared.UUID, state cascade.NodeState, settlingSignalType *string, changed bool,
) error {
	if args.Persist == nil {
		return nil
	}
	rt := args.Persist.NodeRunTree()
	if rt == nil {
		return nil
	}
	return rt.UpdateStateAndOutcome(ctx, tx, runID, state, settlingSignalType, changed)
}

func childStatesForAggregate(children []persistence.NodeRunTreeRow) []ChildState {
	inputs := make([]ChildState, len(children))
	for i, c := range children {
		var sigType *signalpkg.TypePath
		if c.SettlingSignalType != nil {
			tp := signalpkg.TypePath(*c.SettlingSignalType)
			sigType = &tp
		}
		inputs[i] = ChildState{
			State:              c.State,
			SettlingSignalType: sigType,
			Changed:            c.Changed,
		}
	}
	return inputs
}

func Aggregate(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	if len(children) == 0 {
		return AggregateResult{IsSettled: false}
	}
	switch policy.Kind {
	case spec.AggregationKindStrict:
		return aggregateStrict(children, policy)
	case spec.AggregationKindThreshold:
		return aggregateThreshold(children, policy)
	case spec.AggregationKindBestEffort:
		return aggregateBestEffort(children)
	case spec.AggregationKindFirst:
		return aggregateFirst(children)
	}
	return aggregateStrict(children, policy)
}

func aggregateStrict(children []ChildState, policy spec.AggregationPolicy) AggregateResult {
	anyActive := false
	for _, c := range children {
		if c.IsFailure() {
			return AggregateResult{
				IsSettled:                true,
				ParentState:              cascade.NodeStateFailed,
				ParentSettlingSignalType: signalpkg.TypePath("terminal/error/aggregate/strict_failed"),
				Action:                   AggregateActionCancelSiblings,
			}
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
			sig := signalpkg.TypePath("terminal/success")
			if c.SettlingSignalType != nil {
				sig = *c.SettlingSignalType
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

func CreateRootNodeRun(
	ctx context.Context, tx persistence.Tx, rt persistence.NodeRunTreeTable,
	clock shared.Clock,
	nodeID shared.UUID, frameID shared.UUID, runScopeID shared.UUID,
	executor string, requiredClaimProducers []string,
	policy spec.AggregationPolicy,
) (shared.UUID, error) {
	runID := shared.UUID(uuid.New())
	if policy.Kind == "" {
		policy.Kind = spec.AggregationKindStrict
	}
	if err := rt.CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
		NodeRunID:              runID,
		NodeID:                 nodeID,
		FrameID:                frameID,
		RunScopeID:             runScopeID,
		ExecutorName:           executor,
		RequiredClaimProducers: requiredClaimProducers,
		AggregationPolicy:      policy,
		EnqueuedAt:             clock.Now(),
	}); err != nil {
		return shared.UUID{}, fmt.Errorf("CreateRootNodeRun: %w", err)
	}
	return runID, nil
}

func CreateChildNodeRun(
	ctx context.Context, tx persistence.Tx, rt persistence.NodeRunTreeTable, queue persistence.Queue,
	clock shared.Clock,
	nodeID shared.UUID, frameID shared.UUID, runScopeID shared.UUID,
	executor string, requiredClaimProducers []string, policy spec.AggregationPolicy,
) (shared.UUID, error) {
	if queue != nil {
		existing, ok, err := queue.GetInFlightRunForNode(ctx, tx, nodeID, runScopeID)
		if err != nil {
			return shared.UUID{}, fmt.Errorf("CreateChildNodeRun: lookup in-flight: %w", err)
		}
		if ok {
			return existing, nil
		}
	}
	runID := shared.UUID(uuid.New())
	if policy.Kind == "" {
		policy.Kind = spec.AggregationKindStrict
	}
	if err := rt.CreateChildNodeRun(ctx, tx, persistence.CreateChildNodeRunInput{
		NodeRunID:              runID,
		NodeID:                 nodeID,
		FrameID:                frameID,
		RunScopeID:             runScopeID,
		ExecutorName:           executor,
		RequiredClaimProducers: requiredClaimProducers,
		AggregationPolicy:      policy,
		EnqueuedAt:             clock.Now(),
	}); err != nil {
		return shared.UUID{}, fmt.Errorf("CreateChildNodeRun: %w", err)
	}
	return runID, nil
}

func GetRunTree(
	ctx context.Context, tx persistence.Tx, rt persistence.NodeRunTreeTable,
	runID shared.UUID,
) ([]persistence.NodeRunTreeRow, error) {
	root, err := rt.GetByID(ctx, tx, runID)
	if err != nil {
		return nil, fmt.Errorf("GetRunTree: load root: %w", err)
	}
	if root == nil {
		return nil, nil
	}
	out := []persistence.NodeRunTreeRow{*root}
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
			queue = append(queue, c.NodeRunID)
		}
	}
	return out, nil
}
