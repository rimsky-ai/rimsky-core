// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// fanout_dispatch.go — E7. Fan-out dispatch: SplitScope → N sub-claims
// → N child leaf runs.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Fan-out template DSL + §Recursive claim-tree resolution.
//
//	@concept: fan-out
//	@concept: claim-tree
//	@concept: run-scope
//
// Architectural shape:
//
//   - Acquisition-phase (E4, already landed): when a template node
//     declares `fan_out:`, the supervisor's acquire-tx calls
//     `ClaimProducer.SplitScope` on the parent claim and INSERTs one
//     `rimsky_claim_handles` sub-claim row per `SubClaimScopeDescriptor`.
//     `runtime/runner_subclaim.go::AcquireSubClaims` is the helper.
//
//   - Dispatch-phase (this file): post-acquisition, the supervisor
//     projects the acquired sub-claims into partitions and delegates
//     the run-side allocation (per-partition `fanout_partition`
//     RunScope + one child `rimsky_node_runs` row per sub-claim +
//     sub-claim wiring) to the unified
//     `child_execution.go::DispatchChildren` primitive. Each child run
//     dispatches independently to the leaf executor; the parent run
//     aggregates per its `AggregationPolicy` (the standard run-tree
//     aggregation engine in `runtime/state_propagation.go`).
//
//   - Parallelism: when `fan_out.parallelism > 0`, the supervisor
//     limits concurrent in-flight leaves to that cap. Remaining leaves
//     stay in `pending` state until a slot opens. Implementation:
//     a per-parent-run counting semaphore the dispatcher consults
//     before claiming the next child's run row.
//
//   - Terminal-phase: at each leaf's terminal, the supervisor calls
//     `CommitCandidate(producer_candidate_handle)` on success or
//     `AbandonCandidate` on failure / strict-cancel. The standard
//     auto-terminal recursion in `runtime/auto_terminal.go::CheckAndFireResolution`
//     drives the bottom-up resolution; at the parent run's aggregated
//     terminal it fires `ClaimProducer.Commit(parent_handle_id)` for
//     promote (success) or `ClaimProducer.Abandon` for abandon. The
//     producer's `Commit` response (carries `version_id` + producer-
//     supplied metadata bytes) is persisted on the parent run's
//     writeback row + on `rimsky_claim_handles.version_id`.
//
// File status — V1 wiring posture:
//
// The run-side allocation lives in the unified
// `child_execution.go::DispatchChildren` primitive; this file holds
// the fan-out-specific surface — the sub-claim → partition projection,
// the parallelism semaphore, and the thin dispatch wrapper. The
// acquisition path (E4, runner_acquire.go ~408) returns
// `acquisition.SubClaims` when the node declares `fan_out:`; this file
// picks up from there.

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

// FanOutPartitions is the pure projection that turns the acquired
// sub-claim list into the per-partition descriptors `DispatchChildren`
// consumes — one partition per sub-claim, carrying the producer's
// partition key + the sub-claim handle id.
//
// The returned slice ordering matches the input ordering (the
// producer's SubScope ordering). Caller is free to sort by
// `PartitionKey` for deterministic dispatch (the scenario tests do, to
// make assertions reproducible).
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

// FanOutParallelismSemaphore is the per-parent-run counting semaphore
// that limits in-flight leaves to `cap`. Zero / negative `cap` means
// unbounded (no semaphore; every leaf dispatches as soon as its row is
// created).
//
// The semaphore lives in-process per supervisor; multi-replica
// supervisors enforce the cap via the persistence-layer dispatch-row
// claim plus a SELECT counting in-flight rows for the parent. Pre-v1:
// in-process is the documented posture — operator deployments use a
// single supervisor replica when fan-out parallelism is load-bearing.
// The multi-replica posture is the obvious extension once the
// distributed-claim path lands.
//
// Acquire is blocking; Release is non-blocking. Zero-value semaphore
// is uninitialized (panics on Acquire) — callers always invoke
// NewFanOutParallelismSemaphore.
type FanOutParallelismSemaphore struct {
	cap   int
	slots chan struct{}
}

// NewFanOutParallelismSemaphore constructs a semaphore for the given
// cap. cap <= 0 → unbounded.
func NewFanOutParallelismSemaphore(cap int) *FanOutParallelismSemaphore {
	if cap <= 0 {
		return &FanOutParallelismSemaphore{cap: 0}
	}
	return &FanOutParallelismSemaphore{
		cap:   cap,
		slots: make(chan struct{}, cap),
	}
}

// Acquire blocks until a slot is free or the context is cancelled.
// Returns ctx.Err() on cancellation. Unbounded semaphore returns nil
// immediately.
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

// Release returns a slot. Safe to call on an unbounded semaphore (no-op).
func (s *FanOutParallelismSemaphore) Release() {
	if s == nil || s.cap == 0 {
		return
	}
	<-s.slots
}

// InFlight returns the current in-flight count. Used by metrics +
// dispatch-side diagnostics. Returns 0 on unbounded semaphores.
func (s *FanOutParallelismSemaphore) InFlight() int {
	if s == nil || s.cap == 0 {
		return 0
	}
	return len(s.slots)
}

// FanOutSemaphoreRegistry tracks semaphores per parent-run. Lives on
// the supervisor (one registry per process). Lookup is by parent run
// id; the registry creates the semaphore lazily on first lookup with
// the parent's snapshotted `fan_out.parallelism` value.
//
// Concurrency: protected by a mutex. The semaphore handle returned is
// safe for concurrent use across goroutines (acquire / release are
// channel-backed).
type FanOutSemaphoreRegistry struct {
	mu sync.Mutex
	m  map[shared.UUID]*FanOutParallelismSemaphore
}

// NewFanOutSemaphoreRegistry constructs an empty registry.
func NewFanOutSemaphoreRegistry() *FanOutSemaphoreRegistry {
	return &FanOutSemaphoreRegistry{m: make(map[shared.UUID]*FanOutParallelismSemaphore)}
}

// GetOrCreate returns the per-parent semaphore, creating one with
// `cap` slots on first lookup. Subsequent lookups ignore the cap
// argument (the first one wins — the parent's snapshot is canonical).
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

// Drop forgets the per-parent semaphore. Caller invokes at parent-run
// terminal so the map doesn't grow without bound across long-running
// supervisors.
func (r *FanOutSemaphoreRegistry) Drop(parentRunID shared.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, parentRunID)
}

// IsFanOutNode reports whether a template node-def declares fan-out.
// Cheap predicate the dispatcher consults post-acquisition: when true,
// the supervisor invokes `dispatchFanOutChildren` (which delegates to
// `DispatchChildren`) instead of the standard single-run dispatch.
func IsFanOutNode(def *node.TemplateNodeDef) bool {
	return def != nil && def.FanOut != nil
}

// FanOutAggregationPolicy returns the snapshot aggregation policy for
// a fan-out node. Returns the zero value (which the standard
// `Aggregate` function defaults to `strict`) when the node does not
// declare a fan-out.
//
// Spec §Output aggregation: the fan-out node's `error_policy` is the
// per-parent aggregation policy; the run-tree state-propagation
// transaction consults this when each child terminates.
func FanOutAggregationPolicy(def *node.TemplateNodeDef) spec.AggregationPolicy {
	if def == nil || def.FanOut == nil {
		return spec.AggregationPolicy{}
	}
	return def.FanOut.ErrorPolicy
}

// dispatchFanOutChildren is the thin fan-out wrapper over the unified
// `DispatchChildren` primitive. Called post-acquisition by `RunNode`
// when the node declared `fan_out:` (acquireCandidate's E4 hot path
// returned non-empty `acq.SubClaims`).
//
// The function:
//
//  1. Snapshots the aggregation policy from the fan-out spec onto the
//     parent run row (so PropagateFromChildState sees the right rule
//     table when children terminate).
//  2. Builds the `ChildExecutionInput` — N partitions projected from
//     the `AcquireSubClaims` results via `FanOutPartitions`, one child
//     run spec (the parent's own node + executor + stores), entry NOT
//     absorbed.
//  3. Delegates the per-partition RunScope + child-row allocation and
//     sub-claim wiring to `DispatchChildren` — idempotent per
//     (parent_run_id, partition_key).
//  4. Records the dispatch decision in the event log for operator-
//     facing observability.
//
// All persistence calls run in a single tx so the parent's policy
// snapshot, the child row inserts, and the event-log emission commit
// atomically. On failure the tx rolls back and the runner's outer
// caller routes the parent through the standard error-policy path.
//
// The parent run's leaf-dispatch is INTENTIONALLY SKIPPED: per spec
// §Fan-out template DSL "Mechanics at dispatch" step 4, each child
// dispatches independently against the leaf executor; the parent
// holds the parent claim until children settle.
//
//	@concept: fan-out
//	@concept: run-scope
//	@concept: claim-tree
func dispatchFanOutChildren(ctx context.Context, args RunArgs, acq *acquisition) error {
	if acq == nil || acq.NodeDef == nil || acq.NodeDef.FanOut == nil {
		return fmt.Errorf("dispatchFanOutChildren: not a fan-out node")
	}
	policy := FanOutAggregationPolicy(acq.NodeDef)
	in := ChildExecutionInput{
		ParentRunID:      acq.DispatchID,
		ParentRunScopeID: acq.RunScopeID,
		InstanceID:       acq.InstanceID,
		FrameID:          acq.FrameID,
		ChildGraphName:   acq.GraphName,
		// @constraint: child run rows carry the zero policy; the author policy
		// lives on the PARENT row via the snapshot below (that is the
		// row PropagateFromChildState consults at settlement).
		AggregationPolicy: spec.AggregationPolicy{},
		EntryAbsorbed:     false,
		Partitions:        FanOutPartitions(acq.SubClaims),
		Children: []ChildRunSpec{{
			NodeID:         acq.NodeID,
			Executor:       acq.Executor,
			RequiredStores: requiredStoresForAcq(acq),
		}},
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		// @constraint: snapshot the parent's aggregation policy so PropagateFromChildState
		// + Aggregate use the right rule table. UpdateAggregationPolicy
		// is idempotent for a no-op write.
		if err := args.Persist.RunTree().UpdateAggregationPolicy(ctx, tx, acq.DispatchID, policy); err != nil {
			return fmt.Errorf("dispatchFanOutChildren: snapshot policy: %w", err)
		}
		children, err := DispatchChildren(ctx, args, tx, in)
		if err != nil {
			return fmt.Errorf("dispatchFanOutChildren: %w", err)
		}
		// @deliberate: audit-log the fan-out wave for operator observability. Two
		// events fire: the legacy `fan_out_dispatched` (kept for
		// transitioning observers) and the post-2026-05-16 forensics
		// kind `fanout.children_created` summarizing the child-run
		// projection from the parent's perspective. Both honor
		// @blessed-invariant 20 (no scope bytes in the payload).
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
