// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: run-scope
// @concept: terminal-resolution

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type NodeRunTreeRow struct {
	NodeRunID          shared.UUID            `json:"run_id"`
	NodeID             shared.UUID            `json:"node_id"`
	FrameID            shared.UUID            `json:"frame_id"`
	RunScopeID         shared.UUID            `json:"run_scope_id"`
	State              cascade.NodeState      `json:"state"`
	SettlingSignalType *string                `json:"settling_signal_type,omitempty"`
	AggregationPolicy  spec.AggregationPolicy `json:"aggregation_policy,omitempty"`
	Changed            bool                   `json:"changed"`
}

type CreateRootNodeRunInput struct {
	NodeRunID              shared.UUID
	NodeID                 shared.UUID
	FrameID                shared.UUID
	RunScopeID             shared.UUID
	AggregationPolicy      spec.AggregationPolicy
	ExecutorName           string
	RequiredClaimProducers []string
	EnqueuedAt             time.Time
}

type CreateChildNodeRunInput struct {
	NodeRunID              shared.UUID
	NodeID                 shared.UUID
	FrameID                shared.UUID
	RunScopeID             shared.UUID
	ExecutorName           string
	RequiredClaimProducers []string
	AggregationPolicy      spec.AggregationPolicy
	EnqueuedAt             time.Time
}

type NodeRunTreeTable interface {
	CreateRootNodeRun(ctx context.Context, in CreateRootNodeRunInput, tx Tx) error

	CreateChildNodeRun(ctx context.Context, in CreateChildNodeRunInput, tx Tx) error

	GetByID(ctx context.Context, runID shared.UUID, tx Tx) (*NodeRunTreeRow, error)

	LockTreeForUpdate(ctx context.Context, runID shared.UUID, tx Tx) (*NodeRunTreeRow, error)

	ListChildren(ctx context.Context, parentNodeRunID shared.UUID, tx Tx) ([]NodeRunTreeRow, error)

	UpdateStateAndOutcome(ctx context.Context, runID shared.UUID, state cascade.NodeState, settlingSignalType *string, changed bool, tx Tx) error

	UpdateAggregationPolicy(ctx context.Context, runID shared.UUID, policy spec.AggregationPolicy, tx Tx) error
}

func MarshalAggregationPolicy(p spec.AggregationPolicy) ([]byte, error) {
	if p == (spec.AggregationPolicy{}) {
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
