// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope
// @concept: terminal-resolution

package persistence

import (
	"context"
	"encoding/json"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type RunTreeRow struct {
	RunID              shared.UUID            `json:"run_id"`
	NodeID             shared.UUID            `json:"node_id"`
	FrameID            shared.UUID            `json:"frame_id"`
	RunScopeID         shared.UUID            `json:"run_scope_id"`
	State              cascade.NodeState      `json:"state"`
	SettlingSignalType *string                `json:"settling_signal_type,omitempty"`
	AggregationPolicy  spec.AggregationPolicy `json:"aggregation_policy,omitempty"`
}

type CreateRootRunInput struct {
	RunID                  shared.UUID
	NodeID                 shared.UUID
	FrameID                shared.UUID
	RunScopeID             shared.UUID
	AggregationPolicy      spec.AggregationPolicy
	ExecutorName           string
	RequiredClaimProducers []string
}

type CreateChildRunInput struct {
	RunID                  shared.UUID
	NodeID                 shared.UUID
	FrameID                shared.UUID
	RunScopeID             shared.UUID
	ExecutorName           string
	RequiredClaimProducers []string
	AggregationPolicy      spec.AggregationPolicy
}

type RunTreeTable interface {
	CreateRootRun(ctx context.Context, tx Tx, in CreateRootRunInput) error

	CreateChildRun(ctx context.Context, tx Tx, in CreateChildRunInput) error

	GetByID(ctx context.Context, tx Tx, runID shared.UUID) (*RunTreeRow, error)

	LockTreeForUpdate(ctx context.Context, tx Tx, runID shared.UUID) (*RunTreeRow, error)

	ListChildren(ctx context.Context, tx Tx, parentRunID shared.UUID) ([]RunTreeRow, error)

	UpdateStateAndOutcome(ctx context.Context, tx Tx, runID shared.UUID, state cascade.NodeState, settlingSignalType *string) error

	UpdateAggregationPolicy(ctx context.Context, tx Tx, runID shared.UUID, policy spec.AggregationPolicy) error
}

func MarshalAggregationPolicy(p spec.AggregationPolicy) ([]byte, error) {
	if p.Kind == "" && !p.CancelSiblings && p.MaxFailures == 0 {
		return nil, nil
	}
	return json.Marshal(p)
}

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
