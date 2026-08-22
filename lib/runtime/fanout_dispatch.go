// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

//	@concept: fan-out
//	@concept: claim-tree
//	@concept: run-scope

package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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

func (r *FanOutSemaphoreRegistry) GetOrCreate(parentNodeRunID shared.UUID, cap int) *FanOutParallelismSemaphore {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.m[parentNodeRunID]; ok {
		return s
	}
	s := NewFanOutParallelismSemaphore(cap)
	r.m[parentNodeRunID] = s
	return s
}

func (r *FanOutSemaphoreRegistry) Drop(parentNodeRunID shared.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, parentNodeRunID)
}

// @concept: fan-out
func fanOutParallelismSemaphore(ctx context.Context, dctx dispatchContext) *FanOutParallelismSemaphore {
	acq := dctx.Acquired
	if acq == nil || acq.NodeDef == nil || acq.NodeDef.FanOut == nil || acq.NodeDef.FanOut.Parallelism <= 0 {
		return nil
	}
	if dctx.Args.FanOutSemaphores == nil {
		return nil
	}
	scope := resolveAcqScope(ctx, dctx.Args, acq, nil)
	if scope.ParentNodeRunID == nil {
		return nil
	}
	return dctx.Args.FanOutSemaphores.GetOrCreate(*scope.ParentNodeRunID, acq.NodeDef.FanOut.Parallelism)
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
// @decision: fan-out-and-delegation-are-distinct-mechanisms
func dispatchFanOutChildren(ctx context.Context, args RunArgs, acq *acquisition) error {
	if acq == nil || acq.NodeDef == nil || acq.NodeDef.FanOut == nil {
		return fmt.Errorf("dispatchFanOutChildren: not a fan-out node")
	}
	policy := FanOutAggregationPolicy(acq.NodeDef)
	in := ChildExecutionInput{
		ParentNodeRunID:   acq.NodeRunID,
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
		if err := args.Persist.NodeRunTree().UpdateAggregationPolicy(ctx, acq.NodeRunID, policy, tx); err != nil {
			return fmt.Errorf("dispatchFanOutChildren: snapshot policy: %w", err)
		}
		children, err := DispatchChildren(ctx, args, in, tx)
		if err != nil {
			return fmt.Errorf("dispatchFanOutChildren: %w", err)
		}
		// @concept: fan-out
		// @decision: held-as-state-not-phase
		if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeRunID,
			cascade.NodeStateHeld, cascade.ReasonFanoutDispatched, nil, tx); err != nil {
			return fmt.Errorf("dispatchFanOutChildren: parent → held: %w", err)
		}
		if err := AppendStateTransitionEvent(ctx, args.Persist, acq.NodeID, acq.InstanceID,
			cascade.NodeStateRunning, cascade.NodeStateHeld, cascade.ReasonFanoutDispatched, tx); err != nil {
			return fmt.Errorf("dispatchFanOutChildren: state_transition event: %w", err)
		}
		if err := args.Queue.RemoveForNode(ctx, acq.NodeID, acq.RunScopeID, args.SupervisorID, tx); err != nil {
			return fmt.Errorf("dispatchFanOutChildren: release parent queue claim: %w", err)
		}
		childKeys := make([]string, 0, len(children))
		childIDs := make([]string, 0, len(children))
		for _, c := range children {
			childKeys = append(childKeys, c.PartitionKey)
			childIDs = append(childIDs, c.NodeRunID.String())
		}
		if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindFanOutDispatched(),
			Payload: eventpayload.New(&genv1.FanOutDispatchedPayload{
				ParentRunId:  acq.NodeRunID.String(),
				ChildRunIds:  childIDs,
				ChildKeys:    childKeys,
				Parallelism:  int32(acq.NodeDef.FanOut.Parallelism),
				PolicyKind:   string(policy.Kind),
				NumSubClaims: int32(len(acq.SubClaims)),
			}),
		}, tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: events.KindFanoutChildrenCreated(),
			Payload: eventpayload.New(&genv1.FanOutChildrenCreatedPayload{
				ParentRunId:        acq.NodeRunID.String(),
				ParentNodeId:       acq.NodeID.String(),
				ChildCount:         int32(len(children)),
				PartitionKeysCount: int32(len(childKeys)),
			}),
		}, tx)
	})
}
