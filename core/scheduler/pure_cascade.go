// Empty-executor sweep. Per spec §17.1 step 4 (the omnibus runner) the
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
//     runner picks it up via §17.1 step 4b and synthesises the
//     Complete{changed:true} outcome itself once the claim+locks are
//     acquired.
//
// The split is template-driven: the scheduler reads each node's template
// node-def via the storage-backed in-memory template registry to inspect
// `Stores`. When the template / node-type cannot be resolved the row is
// treated as pure-cascade — the historically conservative default (§6.4
// behaviour preserved) and the only path that does not require any
// downstream supervisor coordination.
package scheduler

import (
	"context"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// PureCascadeArgs bundles the dependencies ProcessPureCascade needs.
type PureCascadeArgs struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
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
//     runner (spec §17.1 step 4b) takes it from here.
//
// Errors on individual nodes are logged and processing continues; the
// return value is the count of nodes successfully processed (transitioned
// for pure-cascade, enqueued for native-claim-only). Per spec §6.4 +
// §17.1 step 4.
func ProcessPureCascade(ctx context.Context, args PureCascadeArgs) (int, error) {
	sb := args.Storage
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	ready, err := sb.Nodes().ListPureCascadeReady(ctx, nil)
	if err != nil {
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
func transitionPureCascade(ctx context.Context, args PureCascadeArgs, n storage.NodeRow, log shared.Logger) error {
	sb := args.Storage
	// UpdateState atomically clears frame_id when target state is 'fresh'
	// (per the defensive guard in enforceAndUpdate; spec §4.4 + §10.3,
	// fresh nodes carry no frame_id). No separate SetFrameID call needed.
	if err := sb.Nodes().UpdateState(ctx, n.ID, shared.NodeStateFresh, nodepkg.ReasonPureCascade, nil); err != nil {
		log.Warn("ProcessPureCascade: state transition failed",
			"node_id", n.ID.String(), "error", err.Error())
		return err
	}
	nodeID := n.ID
	instanceID := n.InstanceID
	if err := sb.Events().Append(ctx, storage.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       "pure_cascade_commit",
		Payload:    map[string]any{},
	}, nil); err != nil {
		log.Warn("ProcessPureCascade: append pure_cascade_commit failed",
			"node_id", n.ID.String(), "error", err.Error())
		// Not fatal — the state transition already succeeded.
	}
	dependents, derr := sb.Nodes().ListDependentsOf(ctx, n.ID, nil)
	if derr != nil {
		log.Warn("ProcessPureCascade: list dependents failed",
			"node_id", n.ID.String(), "error", derr.Error())
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
		if rerr := RecalculateNode(ctx, RecalculateArgs{
			Storage:      sb,
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
// accepted_stores`, spec §14.2) routes the row correctly. The node row
// stays `stale` until the supervisor's omnibus runner claims it and
// synthesises the §17.1 step 4b Complete.
func enqueueNativeClaimOnly(ctx context.Context, args PureCascadeArgs, n storage.NodeRow, def *nodepkg.TemplateNodeDef) error {
	required := nodepkg.RequiredStores(*def)
	if required == nil {
		required = []string{}
	}
	// FrameID is sourced from the node row — pure-cascade-flagged stale
	// nodes belong to the running frame (blessed-invariant 19).
	if n.FrameID == nil {
		// Defer: frame engine hasn't advanced the originating frame yet.
		return nil
	}
	return args.Queue.Enqueue(ctx, queue.DispatchRequest{
		NodeID:         n.ID,
		ExecutorName:   "",
		RequiredStores: required,
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        *n.FrameID,
	})
}

// lookupTemplateNodeDef resolves the template node-def for a node row by
// hop: instance → template → node-by-type. Returns nil when any hop is
// missing — degraded-but-valid states a running scheduler must tolerate
// (template/instance deletion, schema drift). Mirrors
// supervisor.findNodeUserdata's contract.
//
// @source: core/supervisor/runner.go:findNodeUserdata
func lookupTemplateNodeDef(ctx context.Context, sb storage.StorageBackend, n storage.NodeRow) *nodepkg.TemplateNodeDef {
	inst, _ := sb.Instances().Get(ctx, n.InstanceID, nil)
	if inst == nil {
		return nil
	}
	tmpl, _ := sb.Templates().Get(ctx, inst.TemplateID, nil)
	if tmpl == nil {
		return nil
	}
	for i := range tmpl.Spec.Nodes {
		if tmpl.Spec.Nodes[i].Type == n.NodeType {
			return &tmpl.Spec.Nodes[i]
		}
	}
	return nil
}

// hasClaimStore reports whether the node-def declares any store
// claim. Under stores-redesign-v2 every NodeStoreRef is a claim
// (selector + intent + alias), so a non-empty Stores list answers yes.
// Returns false when def is nil — the historically conservative
// default (§6.4): if the template can't be resolved, treat the row as
// pure-cascade rather than enqueueing it onto a queue no supervisor
// pool may have specialised for.
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
func cascadePropagateFrameID(ctx context.Context, sb storage.StorageBackend, childID shared.UUID, frameID shared.UUID, log shared.Logger) {
	err := sb.Transaction(ctx, func(ctx context.Context, stx storage.Tx) error {
		pgT, err := pgstorage.PgxTxFromStorage(stx)
		if err != nil {
			return err
		}
		_, err = pgT.Exec(ctx, `
            UPDATE rimsky_nodes
            SET state = 'stale', frame_id = $1, updated_at = now()
            WHERE id = $2
              AND (state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))
        `, frameID, childID)
		return err
	})
	if err != nil && log != nil {
		log.Warn("cascadePropagateFrameID: failed",
			"child_id", childID.String(),
			"frame_id", frameID.String(),
			"error", err.Error())
	}
}
