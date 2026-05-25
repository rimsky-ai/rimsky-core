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
	"errors"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
	signalaudit "github.com/fallguyconsulting/rimsky/foundation/signal/audit"
	nodepkg "github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/runtime"
)

// PureCascadeArgs bundles the dependencies ProcessPureCascade needs.
type PureCascadeArgs struct {
	Persist persistence.Tables
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
				// Defensive: a closed RunScope means the rendezvous
				// fired before the sweep could enqueue. Walker
				// discipline per concept:run-scope: skip silently.
				if errors.Is(err, persistence.ErrRunScopeClosed) {
					log.Debug("ProcessPureCascade: skip native claim-only enqueue: run scope closed",
						"node_id", n.ID.String())
					continue
				}
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
		// Thread the projected RunScope id; pure-cascade nodes are
		// non-executor and don't fan out today, but threading keeps
		// the disambiguation consistent across paths. A node without
		// a projected RunScope has nothing to transition.
		if n.RunScopeID == nil {
			return nil
		}
		// Pure-cascade settles fresh; settling_signal_type carries the
		// terminal/success envelope (per concept:signal). Subscribers
		// that need to distinguish a pure-cascade settle from an
		// executor-Success terminal can match on the audit-event
		// payload (no separate signal-type leaf for pure-cascade pre-v1).
		pureCascadeSig := "terminal/success"
		return sb.Nodes().UpdateState(ctx, n.ID, *n.RunScopeID, cascade.NodeStateFresh, cascade.ReasonPureCascade, &pureCascadeSig, tx)
	}); err != nil {
		log.Warn("ProcessPureCascade: state transition failed",
			"node_id", n.ID.String(), "error", err.Error())
		return err
	}
	nodeID := n.ID
	instanceID := n.InstanceID
	// Canonical terminal/success signal per concept:signal. Pure-
	// cascade transitions are signal-bearing (settled-fresh state);
	// the pre-Pass-5 fixed-string "pure_cascade_commit" audit row
	// retired alongside spec 2026-05-23-signal-taxonomy-and-policy-
	// decoupling-design. Subscribers that need to distinguish
	// pure-cascade from executor-Success can match on the signal
	// payload's `change_summary` ("pure_cascade").
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		successSig := signalpkg.Signal{
			Type: signalpkg.TypePath("terminal/success"),
			Payload: map[string]any{
				"changed":          false,
				"attributes_delta": map[string]any{},
				"change_summary":   "pure_cascade",
			},
		}
		return signalaudit.EmitSignal(ctx, sb.Events(), instanceID, nodeID, successSig, args.Clock.Now(), tx)
	}); err != nil {
		log.Warn("ProcessPureCascade: emit terminal/success signal failed",
			"node_id", n.ID.String(), "error", err.Error())
		// Not fatal — the state transition already succeeded.
	}
	// Post-2026-05-14: receivers resolved from the per-template
	// subscription-edge inverse map; the retired nodes.dependencies
	// column is no longer consulted.
	var receivers []persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := sb.Instances().Get(ctx, n.InstanceID, tx)
		if err != nil || inst == nil {
			return err
		}
		row, err := sb.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		if err != nil || row == nil {
			return err
		}
		subs := nodepkg.ExtractSubstitutionRefsFromTemplate(row.Spec)
		edges, err := nodepkg.BuildSubscriptionEdges(row.Spec, subs)
		if err != nil {
			return err
		}
		if edges == nil {
			return nil
		}
		receiverTypeList := edges.ReceiverNodeTypesForSender(n.NodeType)
		if len(receiverTypeList) == 0 {
			return nil
		}
		want := make(map[string]struct{}, len(receiverTypeList))
		for _, t := range receiverTypeList {
			want[t] = struct{}{}
		}
		instNodes, err := sb.Nodes().ListByInstance(ctx, n.InstanceID, tx)
		if err != nil {
			return err
		}
		for _, x := range instNodes {
			if x.ID == n.ID {
				continue
			}
			if _, ok := want[x.NodeType]; ok {
				receivers = append(receivers, x)
			}
		}
		return nil
	}); err != nil {
		log.Warn("ProcessPureCascade: list receivers failed",
			"node_id", n.ID.String(), "error", err.Error())
		return nil
	}
	// Cascade message-pass: mark each receiver stale + parent's frame_id
	// (per spec §4.4). The pure-cascade source's frame_id was set at
	// frame-start; receivers inherit it so the frame-end predicate (§4.2)
	// sees them as in-flight and the next sweep enqueues their dispatch.
	//
	// Per spec §"RunScope-first cascade", same-scope cascade affirms a
	// pending row for the receiver in the source's RunScope so the
	// receiver's RunScopeID projection lands before the recalculate
	// enqueue. Without this, a stale-no-in-flight receiver loses the
	// frame-and-scope binding and the dispatch enqueue errors with
	// run_scope_id required.
	var sourceRunScopeID shared.UUID
	if n.RunScopeID != nil {
		sourceRunScopeID = *n.RunScopeID
	}
	for _, dep := range receivers {
		if n.FrameID != nil && sourceRunScopeID != (shared.UUID{}) {
			affirmErr := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return sb.Nodes().AffirmNodeRunRow(ctx, dep.ID, sourceRunScopeID, *n.FrameID, tx)
			})
			// Defensive: a closed RunScope means the receiver's scope
			// has terminated (parent rendezvous has fired). The walker
			// MUST NOT cross into closed RunScopes — skip this receiver
			// and continue per concept:run-scope. Mirror of the pattern
			// at runtime/message_delivery.go::cascadeMessageSubscribersInTx.
			// Without this skip a downstream cascadePropagateFrameID +
			// RecalculateNode would enqueue a new in-flight row into a
			// closed RunScope.
			if errors.Is(affirmErr, persistence.ErrRunScopeClosed) {
				continue
			}
			if affirmErr != nil {
				log.Warn("ProcessPureCascade: affirm receiver run row failed",
					"source_node_id", n.ID.String(),
					"target_node_id", dep.ID.String(),
					"error", affirmErr.Error())
			}
		}
		if n.FrameID != nil {
			cascadePropagateFrameID(ctx, sb, args.Queue, dep.ID, *n.FrameID, log)
		}
		srcID := n.ID
		if rerr := runtime.RecalculateNode(ctx, runtime.RecalculateArgs{
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
	if n.RunScopeID == nil {
		// Defer: Phase B cascade allocator hasn't materialized a
		// RunScope for this node yet.
		return nil
	}
	return args.Queue.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         n.ID,
		ExecutorName:   "",
		RequiredStores: required,
		EnqueuedAt:     args.Clock.Now(),
		FrameID:        *n.FrameID,
		RunScopeID:     *n.RunScopeID,
	})
}

// lookupTemplateNodeDef resolves the template node-def for a node row by
// hop: rimsky_nodes.instance_id → rimsky_instances.template_hash →
// rimsky_templates.spec → node-by-type.
func lookupTemplateNodeDef(ctx context.Context, sb persistence.Tables, n persistence.NodeRow) *nodepkg.TemplateNodeDef {
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
func cascadePropagateFrameID(ctx context.Context, sb persistence.Tables, queue persistence.Queue, childID shared.UUID, frameID shared.UUID, log shared.Logger) {
	err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		child, err := sb.Nodes().Get(ctx, childID, tx)
		if err != nil || child == nil {
			return err
		}
		if child.RunScopeID == nil {
			// No in-flight RunScope projected on the child; the
			// Phase B cascade allocator is responsible for affirming
			// a row before MarkStaleForCascade can apply.
			return nil
		}
		runID, ok, err := queue.GetInFlightRunForNode(ctx, tx, child.ID, *child.RunScopeID)
		if err != nil || !ok {
			return err
		}
		return sb.Nodes().MarkStaleForCascade(ctx, runID, frameID, tx)
	})
	if err != nil && log != nil {
		log.Warn("cascadePropagateFrameID: failed",
			"child_id", childID.String(),
			"frame_id", frameID.String(),
			"error", err.Error())
	}
}
