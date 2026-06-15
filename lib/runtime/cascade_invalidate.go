// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cascade propagation: the cascade-of-stale walk handler. Marks
// dependents stale and recurses on `fresh_changed`. Driven by
// `concept:message` (every cascade walk fires from a sender that is
// settling in the running frame).
//
// The operator-API `node:invalidate` route retired with the 2026-06-14
// message-schema-layer reshape — operators who want to invalidate post a
// typed message via `POST /instances/{id}/messages` with a
// template-declared `messages:` type, and ad-hoc force-stale lives at the
// gated `POST /debug/override` endpoint. The cascade-walk helpers below
// remain because in-frame stale-mark + cascade-walk is still load-bearing
// for the heartbeat-loss recovery path (`code:conductor.go::tick`),
// the parked-resume cascade (`code:wake_parked.go::wakeParkedNode`),
// and the hard-dep pull during a terminal cascade
// (`code:runner_terminal.go::cascadeSubscribersStaleInTx`).
//
// @concept: cascade
package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// invalidationCascadeSignal is the synthetic signal cascade-from-
// invalidation walks emit when they don't have a real terminal signal
// in hand (the heartbeat-loss recovery walk and the parked-resume walk).
// Modeled as a terminal/success with changed: true so existing
// subscriber CEL predicates that gate on `payload.changed` (the common
// case) match; subscribers that want finer-grained filtering should
// subscribe to specific signal shapes instead.
var invalidationCascadeSignal = signalpkg.Signal{
	Type: "terminal/success",
	Payload: map[string]any{
		"changed":          true,
		"attributes_delta": map[string]any{},
		"change_summary":   "invalidation_cascade",
	},
}

// walkCascadeForInvalidatedNode invokes the runtime cascade walk for a
// node that just transitioned from a settled state into stale/running.
// Loads the node's type via a tx-bound read (the persistence-side
// MarkStaleForCascade does not return the type) then drives the BFS
// walk over the subscription edge map.
//
// Called by `code:conductor.go::tick` on the heartbeat-loss recovery
// path and by `code:wake_parked.go::wakeParkedNode` after a parked node
// transitions back to stale; the cascade walker then gates downstream
// subscribers on the just-invalidated sender.
//
// Before the downstream walk, the invalidated node's OWN
// `force_upstream_refresh: true` upstreams are pulled into the same
// frame via pullForceRefreshUpstreams — so any direct-invalidate path
// that lands here drags the node's declared upstream-refresh sources
// into the frame, matching the story:upstream-pull-on-invalidate
// acceptance ("when A is invalidated and X has not been independently
// invalidated, A's substitution context at dispatch contains X's
// freshest value"). Without this, the upstream-refresh pull would fire
// only when A is reached as a receiver of some OTHER sender's cascade
// walk — direct invalidation against A would leave X stale and A would
// dispatch with the prior value.
//
// Placed in cascade_invalidate.go so the cascade-on-invalidation entry
// points can call it without depending on runtime/runner_terminal.go's
// internal acquisition shape. The `queue` parameter is required so
// the BFS walk can route parked receivers through
// wakeParkedReceiverInTx (which dereferences args.Queue); pass the
// caller's persistence.Queue handle through.
//
//	@concept: cascade
//	@concept: wait-set
//	@story: upstream-pull-on-invalidate
func walkCascadeForInvalidatedNode(
	ctx context.Context, sb persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	logger shared.Logger,
	senderNodeID, instanceID, frameID shared.UUID,
) error {
	args := RunArgs{Persist: sb, Queue: queue, Logger: logger}
	n, err := sb.Nodes().Get(ctx, senderNodeID, tx)
	if err != nil || n == nil {
		return err
	}
	// @constraint: resolve the sender's in-flight run id for the post-stage-5 wait-set
	// (rimsky_wait_set keys on the sender's run id). When the sender has
	// no in-flight row in this frame the cascade walk has nothing to
	// gate on; bail out quietly.
	//
	// Under RunScope-first the in-flight resolver keys on
	// (node_id, run_scope_id). The sender's RunScope is projected on
	// NodeRow.RunScopeID; absent (no in-flight run) we bail out.
	if n.RunScopeID == nil {
		return nil
	}
	senderRunID, ok, err := queue.GetInFlightRunForNode(ctx, tx, senderNodeID, *n.RunScopeID)
	if err != nil {
		return fmt.Errorf("walkCascadeForInvalidatedNode: resolve sender run: %w", err)
	}
	if !ok {
		return nil
	}
	// @constraint: pull the just-invalidated node's own force_upstream_refresh
	// upstreams into the frame before the downstream walk. This is the
	// honest implementation of story:upstream-pull-on-invalidate — the
	// invalidate path is the only direct entry point for "A is
	// invalidated"; without this site the upstream-refresh edges A
	// declared would fire only when A is reached as a receiver of some
	// OTHER sender's cascade walk. We load the instance once to resolve
	// the template hash and the instance's node-by-type index, then
	// invoke pullForceRefreshUpstreams in this same tx. Errors propagate
	// — pulling A's upstreams is part of the invalidate's contract, not
	// a best-effort hint. The pull uses its own fresh `visited` set
	// because the downstream BFS in cascadeSubscribersStaleInTx below
	// owns a separate one rooted at A.
	inst, err := sb.Instances().Get(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("walkCascadeForInvalidatedNode: get instance: %w", err)
	}
	if inst != nil {
		instNodes, err := sb.Nodes().ListByInstance(ctx, instanceID, tx)
		if err != nil {
			return fmt.Errorf("walkCascadeForInvalidatedNode: list instance nodes: %w", err)
		}
		byType := make(map[string][]persistence.NodeRow, len(instNodes))
		for _, in := range instNodes {
			byType[in.NodeType] = append(byType[in.NodeType], in)
		}
		visited := map[shared.UUID]struct{}{senderNodeID: {}}
		if err := pullForceRefreshUpstreams(
			ctx, args, tx, *n, byType,
			senderRunID, *n.RunScopeID, frameID,
			inst.TemplateHash, visited,
		); err != nil {
			return err
		}
	}
	return cascadeSubscribersStaleInTx(ctx, args, tx,
		senderNodeID, n.NodeType, senderRunID, instanceID, frameID,
		invalidationCascadeSignal)
}

// stalemarkAndEnqueueInFrame stale-marks `target` in `frameID` inside
// the caller-owned tx, emits a `state_transition` audit event with
// reason `upstream_refresh_pull` (only when the stale-mark actually
// inserted a new run row), then recursively walks the cascade so the
// just-stale upstream's own subscribers (within this frame) are gated
// on it too.
//
// Called by `code:runner_terminal.go::cascadeSubscribersStaleInTx`
// during the hard-dep upstream pull. Runs inside the caller-owned tx
// rather than opening its own (the cascade walker runs in an outer tx,
// and opening a nested tx would self-deadlock under SQLite's single-conn
// pool).
//
// targetRunScopeID is the RunScope id the caller just affirmed for the
// target via AffirmNodeRunRow — the target's projected RunScopeID on
// the NodeRow may be stale (loaded before the affirm), so the caller
// MUST thread the freshly-affirmed scope id through rather than reading
// off the projection.
//
// Idempotency: when the target already has an in-flight run, the
// UPDATE is a no-op; we still skip the audit event AND the recursive
// walk on the no-op branch because the earlier BFS visit handled both.
//
// Recursion choice: when the stale-mark fires the helper MUST call
// walkCascadeForInvalidatedNode. Skipping the recursion would gate the
// upstream itself but leave its own subscribers ungated within this
// frame, breaking the cascade's single-frame-drain property.
//
//	@concept: cascade
//	@concept: attribute
func stalemarkAndEnqueueInFrame(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	target *persistence.NodeRow, targetRunScopeID shared.UUID, frameID shared.UUID,
) error {
	runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, target.ID, targetRunScopeID)
	if err != nil {
		return fmt.Errorf("stalemarkAndEnqueueInFrame: resolve in-flight run %s: %w", target.ID, err)
	}
	if !ok {
		// @deliberate: no in-flight row — earlier visit (if any) already drove the
		// audit event + recursion; skip.
		return nil
	}
	if err := args.Persist.Nodes().MarkStaleForCascade(ctx, runID, frameID, tx); err != nil {
		return fmt.Errorf("stalemarkAndEnqueueInFrame: mark stale %s: %w", target.ID, err)
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &target.ID, InstanceID: &target.InstanceID,
		Kind: events.KindStateTransition(),
		Payload: map[string]any{
			"from":     "fresh",
			"to":       "stale",
			"reason":   "upstream_refresh_pull",
			"frame_id": frameID.String(),
		},
	}, tx); err != nil {
		return fmt.Errorf("stalemarkAndEnqueueInFrame: append event %s: %w", target.ID, err)
	}
	return walkCascadeForInvalidatedNode(ctx, args.Persist, args.Queue, tx,
		args.Logger, target.ID, target.InstanceID, frameID)
}
