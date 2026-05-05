// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Empty-executor sweep. Per spec §7.3 step 4 (the omnibus runner) the
// supervisor distinguishes three dispatch paths inside one runner; the
// scheduler-side sweep here is the upstream-equivalent split for the
// empty-executor rows that the supervisor never sees:
//
//   - Pure cascade (executor empty, no claim store on the template node):
//     the node has no work to do — its only job is to express dependency
//     fan-out. The scheduler flips it stale->fresh inline, logs a
//     pure_cascade_commit event, and emits recalculate to dependents.
//     It never enqueues. (Spec §6.1 step 3 / §6.4.)
//   - Native claim-only (executor empty, at least one stores entry with
//     claim=true): the node has real work — it owns a claim acquisition
//     and lock orchestration. The scheduler enqueues it onto the dispatch
//     queue just like an executor-backed node; the supervisor's omnibus
//     runner picks it up via §7.3 step 4b and synthesises the
//     Complete{changed:true} outcome itself once the claim+locks are
//     acquired.
//
// The split is template-driven: the scheduler reads each node's template
// node-def via the persistence-backed in-memory template registry to
// inspect `Stores`. When the template / node-type cannot be resolved the
// row is treated as pure-cascade — the historically conservative default
// (§6.4 behaviour preserved) and the only path that does not require any
// downstream supervisor coordination.
package scheduler

import (
	"context"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/persistence"
	nodepkg "github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// PureCascadeArgs bundles the dependencies ProcessPureCascade needs.
type PureCascadeArgs struct {
	Persist persistence.Store
	Queue   persistence.Queue
	Clock   shared.Clock
	Logger  shared.Logger
}

// ProcessPureCascade processes the empty-executor stale-with-deps-fresh
// sweep. For each candidate the function consults the template registry
// to classify the node as pure-cascade or native-claim-only:
//
//   - Pure-cascade: transition `stale → fresh` under reason
//     `pure_cascade`, append `pure_cascade_commit`, and emit recalculate
//     to every dependent. The node never enters the dispatch queue.
//   - Native-claim-only: enqueue a dispatch row with the node-def's
//     RequiredStores, leaving the node `stale`. The supervisor's omnibus
//     runner (spec §7.3 step 4b) takes it from here.
//
// Errors on individual nodes are logged and processing continues; the
// return value is the count of nodes successfully processed (transitioned
// for pure-cascade, enqueued for native-claim-only). Per spec §6.4 +
// §7.3 step 4.
func ProcessPureCascade(ctx context.Context, args PureCascadeArgs) (int, error) {
	sb := args.Persist
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	var ready []persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := sb.Nodes().ListPureCascadeReady(ctx, tx)
		ready = rows
		return err
	}); err != nil {
		return 0, err
	}

	count := 0
	for _, n := range ready {
		def := lookupTemplateNodeDef(ctx, sb, n)
		if hasClaimStore(def) {
			if err := enqueueNativeClaimOnly(ctx, args, n, def); err != nil {
				log.Warn("ProcessPureCascade: enqueue native claim-only failed",
					"node_id", n.ID.String(), "error", err.Error())
				continue
			}
			count++
			continue
		}
		if err := transitionPureCascade(ctx, args, n, log); err != nil {
			// transitionPureCascade already logged; treat as not-counted.
			continue
		}
		count++
	}
	return count, nil
}

// transitionPureCascade flips the node `stale → fresh` inline, appends the
// commit event, and emits recalculate to every dependent. Returns an error
// only when the state transition itself fails (the scheduler bails on
// this node so a future tick can retry); event-append and per-dependent
// recalculate failures are logged and swallowed because the state
// transition has already succeeded.
func transitionPureCascade(ctx context.Context, args PureCascadeArgs, n persistence.NodeRow, log shared.Logger) error {
	sb := args.Persist
	// UpdateState atomically clears frame_id when target state is 'fresh'
	// (per the defensive guard in enforceAndUpdate; spec §4.4 + §10.3,
	// fresh nodes carry no frame_id). No separate SetFrameID call needed.
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sb.Nodes().UpdateState(ctx, n.ID, shared.NodeStateFresh, cascade.ReasonPureCascade, tx)
	}); err != nil {
		log.Warn("ProcessPureCascade: state transition failed",
			"node_id", n.ID.String(), "error", err.Error())
		return err
	}
	nodeID := n.ID
	instanceID := n.InstanceID
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sb.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     &nodeID,
			InstanceID: &instanceID,
			Kind:       "pure_cascade_commit",
			Payload:    map[string]any{},
		}, tx)
	}); err != nil {
		log.Warn("ProcessPureCascade: append pure_cascade_commit failed",
			"node_id", n.ID.String(), "error", err.Error())
		// Not fatal — the state transition already succeeded.
	}
	var dependents []persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := sb.Nodes().ListDependentsOf(ctx, n.ID, tx)
		dependents = rows
		return err
	}); err != nil {
		log.Warn("ProcessPureCascade: list dependents failed",
			"node_id", n.ID.String(), "error", err.Error())
		return nil
	}
	// Cascade message-pass: mark each child stale + parent's frame_id
	// (per spec §4.4). The pure-cascade source's frame_id was set at
	// frame-start; children inherit it so the frame-end predicate (§4.2)
	// sees them as in-flight and the next sweep enqueues their dispatch.
	for _, dep := range dependents {
		if n.FrameID != nil {
			cascadePropagateFrameID(ctx, sb, dep.ID, *n.FrameID, log)
		}
		srcID := n.ID
		if rerr := integration.RecalculateNode(ctx, integration.RecalculateArgs{
			Persist:      sb,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       log,
			SourceNodeID: &srcID,
			TargetNodeID: dep.ID,
		}); rerr != nil {
			log.Warn("ProcessPureCascade: recalculate failed",
				"source_node_id", n.ID.String(),
				"target_node_id", dep.ID.String(),
				"error", rerr.Error())
			// Keep going — one failed propagation shouldn't block others.
		}
	}
	return nil
}

// enqueueNativeClaimOnly inserts a dispatch row for a native claim-only
// node. ExecutorName is left empty (the postgres impl maps that to NULL,
// which the supervisor's SelectCandidates accepts via the `executor_name
// IS NULL` branch). RequiredStores is populated from the template node
// def so the supervisor-pool predicate (`required_stores ⊆
// accepted_stores`, spec §6.2) routes the row correctly. The node row
// stays `stale` until the supervisor's omnibus runner claims it and
// synthesises the §7.3 step 4b Complete.
func enqueueNativeClaimOnly(ctx context.Context, args PureCascadeArgs, n persistence.NodeRow, def *nodepkg.TemplateNodeDef) error {
	required := nodepkg.RequiredStores(*def)
	if required == nil {
		required = []string{}
	}
	if n.FrameID == nil {
		// Defer: frame engine hasn't advanced the originating frame yet.
		return nil
	}
	return args.Queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         n.ID,
		ExecutorName:   "",
		RequiredStores: required,
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        *n.FrameID,
	})
}

// lookupTemplateNodeDef resolves the template node-def for a node row by
// hop: rimsky_nodes.instance_id → rimsky_instances.template_hash →
// rimsky_templates.spec → node-by-type.
func lookupTemplateNodeDef(ctx context.Context, sb persistence.Store, n persistence.NodeRow) *nodepkg.TemplateNodeDef {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, n.InstanceID, tx)
		if err != nil || i == nil {
			return err
		}
		inst = i
		t, err := sb.Templates().GetByHash(ctx, i.TemplateHash, tx)
		tmpl = t
		return err
	})
	if inst == nil || tmpl == nil {
		return nil
	}
	for i := range tmpl.Spec.Nodes {
		if tmpl.Spec.Nodes[i].Type == n.NodeType {
			return &tmpl.Spec.Nodes[i]
		}
	}
	return nil
}

// hasClaimStore reports whether the node-def declares any store claim.
func hasClaimStore(def *nodepkg.TemplateNodeDef) bool {
	if def == nil {
		return false
	}
	return len(def.Stores) > 0
}

// cascadePropagateFrameID marks a child node stale + frame_id when it's
// in a state the cascade can advance: 'fresh' (canonical, §4.4) or
// 'stale' with no frame_id (initial-create case where the cascade is the
// first time this child enters the engine). No-op otherwise.
//
// Does NOT use UpdateState because the state machine rejects
// fresh→stale via reasons unknown to it; the cascade message-pass is
// the spec's mandated direct write. Errors are logged + swallowed so a
// failed propagation to one child does not block siblings.
func cascadePropagateFrameID(ctx context.Context, sb persistence.Store, childID shared.UUID, frameID shared.UUID, log shared.Logger) {
	err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sb.Nodes().MarkStaleForCascade(ctx, childID, frameID, tx)
	})
	if err != nil && log != nil {
		log.Warn("cascadePropagateFrameID: failed",
			"child_id", childID.String(),
			"frame_id", frameID.String(),
			"error", err.Error())
	}
}
