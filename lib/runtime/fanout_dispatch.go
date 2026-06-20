// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//	@concept: fan-out
//	@concept: claim-tree
//	@concept: run-scope

package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func FanOutPartitions(subClaims []SubClaim) []PartitionDescriptor {
	out := make([]PartitionDescriptor, 0, len(subClaims))
	for _, sc := range subClaims {
		out = append(out, PartitionDescriptor{
			PartitionKey:     sc.PartitionKey,
			SubClaimHandleID: sc.ClaimHandleID,
		})
	}
	return out
}

type FanOutParallelismSemaphore struct {
	cap   int
	slots chan struct{}
}

func NewFanOutParallelismSemaphore(cap int) *FanOutParallelismSemaphore {
	if cap <= 0 {
		return &FanOutParallelismSemaphore{cap: 0}
	}
	return &FanOutParallelismSemaphore{
		cap:   cap,
		slots: make(chan struct{}, cap),
	}
}

func (s *FanOutParallelismSemaphore) Acquire(ctx context.Context) error {
	if s == nil || s.cap == 0 {
		return nil
	}
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *FanOutParallelismSemaphore) Release() {
	if s == nil || s.cap == 0 {
		return
	}
	<-s.slots
}

func (s *FanOutParallelismSemaphore) InFlight() int {
	if s == nil || s.cap == 0 {
		return 0
	}
	return len(s.slots)
}

type FanOutSemaphoreRegistry struct {
	mu sync.Mutex
	m  map[shared.UUID]*FanOutParallelismSemaphore
}

func NewFanOutSemaphoreRegistry() *FanOutSemaphoreRegistry {
	return &FanOutSemaphoreRegistry{m: make(map[shared.UUID]*FanOutParallelismSemaphore)}
}

func (r *FanOutSemaphoreRegistry) GetOrCreate(parentRunID shared.UUID, cap int) *FanOutParallelismSemaphore {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.m[parentRunID]; ok {
		return s
	}
	s := NewFanOutParallelismSemaphore(cap)
	r.m[parentRunID] = s
	return s
}

func (r *FanOutSemaphoreRegistry) Drop(parentRunID shared.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, parentRunID)
}

func IsFanOutNode(def *node.TemplateNodeDef) bool {
	return def != nil && def.FanOut != nil
}

func FanOutAggregationPolicy(def *node.TemplateNodeDef) spec.AggregationPolicy {
	if def == nil || def.FanOut == nil {
		return spec.AggregationPolicy{}
	}
	return def.FanOut.ErrorPolicy
}

// @concept: fan-out
// @concept: run-scope
// @concept: claim-tree
func dispatchFanOutChildren(ctx context.Context, args RunArgs, acq *acquisition) error {
	if acq == nil || acq.NodeDef == nil || acq.NodeDef.FanOut == nil {
		return fmt.Errorf("dispatchFanOutChildren: not a fan-out node")
	}
	policy := FanOutAggregationPolicy(acq.NodeDef)
	in := ChildExecutionInput{
		ParentRunID:       acq.DispatchID,
		ParentRunScopeID:  acq.RunScopeID,
		InstanceID:        acq.InstanceID,
		FrameID:           acq.FrameID,
		ChildGraphName:    acq.GraphName,
		AggregationPolicy: spec.AggregationPolicy{},
		EntryAbsorbed:     false,
		Partitions:        FanOutPartitions(acq.SubClaims),
		Children: []ChildRunSpec{{
			NodeID:                 acq.NodeID,
			Executor:               acq.Executor,
			RequiredClaimProducers: requiredClaimProducersForAcq(acq),
		}},
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := args.Persist.RunTree().UpdateAggregationPolicy(ctx, tx, acq.DispatchID, policy); err != nil {
			return fmt.Errorf("dispatchFanOutChildren: snapshot policy: %w", err)
		}
		children, err := DispatchChildren(ctx, args, tx, in)
		if err != nil {
			return fmt.Errorf("dispatchFanOutChildren: %w", err)
		}
		childKeys := make([]string, 0, len(children))
		childIDs := make([]string, 0, len(children))
		for _, c := range children {
			childKeys = append(childKeys, c.PartitionKey)
			childIDs = append(childIDs, c.RunID.String())
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindFanOutDispatched(),
			Payload: map[string]any{
				"parent_run_id":  acq.DispatchID.String(),
				"child_run_ids":  childIDs,
				"child_keys":     childKeys,
				"parallelism":    acq.NodeDef.FanOut.Parallelism,
				"policy_kind":    policy.Kind,
				"num_sub_claims": len(acq.SubClaims),
			},
		}, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindFanoutChildrenCreated(),
			Payload: map[string]any{
				"parent_run_id":        acq.DispatchID.String(),
				"parent_node_id":       acq.NodeID.String(),
				"child_count":          len(children),
				"partition_keys_count": len(childKeys),
			},
		}, tx)
	})
}
