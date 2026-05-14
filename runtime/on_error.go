// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Spec §4.2 (on_error) + §7.3 (policy chain). Consults the node's
// error_types policy chain, evaluates an occurrence, persists the resolved
// EvaluatorState, logs an `error` event, and applies the resolved action
// (retry / invalidate / give_up).
//
// Routes every error class through one path. The new
// `template_resolution_failed` (spec §10.4) and `attributes_schema_failed`
// (spec §9.4) classes have no special-case handlers here: when a template
// declares a policy override for them it flows through `lookupPolicy` →
// `node.Evaluate`; absent an override, `node.Evaluate` (policy == nil)
// defaults to give_up("unknown_error_class"), which is exactly the
// `[ {give_up} ]` default §10.4 calls for. Templates may override either
// class via the standard `error_types` block.
//
// Concurrency-tag plumbing is gone — the redesign expresses concurrency
// through named locks declared on the node and configured in
// `named_locks:`; the runner builds and acquires those during dispatch
// (§7.3) and the queue row carries `required_stores` rather than tags.
package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
)

// OnErrorArgs is the payload for OnError.
type OnErrorArgs struct {
	// Persist is the unified persistence.Tables handle (rimsky_* tables).
	Persist persistence.Tables
	// Queue is the dispatch-queue accessor.
	Queue      persistence.Queue
	Clock      shared.Clock
	Logger     shared.Logger
	NodeID     shared.UUID
	InstanceID shared.UUID
	// SupervisorID identifies the supervisor handling the current run. When
	// non-empty, queue deletes (RemoveForNode) are claimant-guarded so a stale
	// sweep from a different supervisor can't accidentally drop our row.
	SupervisorID string
	ErrorClass   string
	Payload      map[string]any
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I3). Threaded through to InvalidateNode call sites
	// fired from the policy chain so `rimsky_invalidates_total` covers
	// policy_invalidate fan-out. Nil → no-op.
	Metrics MetricsHook
}

// OnError evaluates the node's policy for ErrorClass and applies the
// resolved action. See spec §4.2 (on_error), §7.3 (policy chain).
//
// retry     → persist advanced EvaluatorState, transition running→stale
//
//	(reason policy_retry), re-enqueue dispatch with future enqueued_at
//	reflecting the backoff delay.
//
// invalidate → persist advanced EvaluatorState, transition running→stale
//
//	(reason policy_invalidate), resolve each target node type to a node
//	ID within the same instance, and route InvalidateNode to
//	each resolved target. Unresolved targets are logged via the
//	unresolved_invalidate_target event.
//
// give_up   → transition → failed (reason policy_give_up). This is also
//
//	the §10.4 / §9.4 default for `template_resolution_failed` and
//	`attributes_schema_failed` when the template declares no override
//	(via the policy == nil branch of node.Evaluate).
func OnError(ctx context.Context, args OnErrorArgs) error {
	sb, log := args.Persist, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	var nd *persistence.NodeRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		n, err := sb.Nodes().Get(ctx, args.NodeID, tx)
		nd = n
		return err
	}); err != nil {
		return err
	}
	if nd == nil {
		return nil
	}

	// Resolve the per-error-class policy from the template spec.
	policy, err := lookupPolicy(ctx, sb, nd, args.ErrorClass)
	if err != nil {
		return err
	}

	state := node.EvaluatorState{
		ActionIndex:       nd.ActionIndex,
		RetryCounter:      nd.RetryCounter,
		CurrentErrorClass: nd.CurrentErrorClass,
	}
	resolved := node.Evaluate(policy, state, args.ErrorClass, nil)

	// Persist resolved EvaluatorState + log the occurrence in one short tx.
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Nodes().UpdateError(ctx, args.NodeID, resolved.NewState, tx); err != nil {
			return err
		}
		return sb.Events().Append(ctx, persistence.EventAppendInput{
			InstanceID: &args.InstanceID,
			NodeID:     &args.NodeID,
			Kind:       "error",
			Payload: map[string]any{
				"error_class":  args.ErrorClass,
				"details":      args.Payload,
				"action_taken": resolved.Kind,
				"action_index": resolved.NewState.ActionIndex,
				"delay_ms":     resolved.DelayMs,
			},
		}, tx)
	}); err != nil {
		return err
	}

	switch resolved.Kind {
	case "retry", "discard_then_retry", "resume_then_retry":
		if nd.State == cascade.NodeStateRunning {
			if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if err := sb.Nodes().UpdateState(ctx, args.NodeID, cascade.NodeStateStale, cascade.ReasonPolicyRetry, "", tx); err != nil {
					return err
				}
				// Pessimistic-invalidate: running → stale is this
				// sender's invalidation in this frame. Gate downstream
				// subscribers across the retry round-trip.
				//
				//	@concept: cascade
				//	@concept: wait-set
				if nd.FrameID != nil {
					return walkCascadeForInvalidatedNode(ctx, sb, args.Queue, tx, log, args.NodeID, nd.InstanceID, *nd.FrameID)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		_ = args.Queue.RemoveForNode(ctx, args.NodeID, args.SupervisorID)
		if nd.FrameID == nil {
			return fmt.Errorf("OnError retry: node %s has nil frame_id", args.NodeID)
		}
		return args.Queue.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:         args.NodeID,
			ExecutorName:   nd.Executor,
			RequiredStores: requiredStoresForNode(ctx, sb, nd),
			EnqueuedAt:     args.Clock.Now().Add(time.Duration(resolved.DelayMs) * time.Millisecond),
			FrameID:        *nd.FrameID,
		})

	case "give_up":
		if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := sb.Nodes().UpdateState(ctx, args.NodeID, cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, cascade.LastOutcomeFailed, tx); err != nil {
				return err
			}
			// Settled-state drain on failed: any wait-set rows gating
			// receivers on this sender release.
			//
			//	@concept: wait-set
			if nd.FrameID != nil {
				return sb.WaitSet().DeleteBySender(ctx, *nd.FrameID, args.NodeID, tx)
			}
			return nil
		}); err != nil {
			return err
		}
		_ = args.Queue.RemoveForNode(ctx, args.NodeID, args.SupervisorID)
	}
	// `invalidate` action retired under the 2026-05-14 subscription-
	// cascade resolution; the validator rejects it at deploy time.
	return nil
}

// lookupPolicy resolves the error_types[ErrorClass] block from the node's
// template spec. Returns nil (with nil error) when the template does not
// declare a policy for this error class — node.Evaluate treats that as a
// give_up("unknown_error_class"). For the new redesign error classes
// (`template_resolution_failed`, `attributes_schema_failed`) this is the
// §10.4 / §9.4 default chain `[ {give_up} ]`.
func lookupPolicy(ctx context.Context, sb persistence.Tables, nd *persistence.NodeRow, errorClass string) (*node.ErrorTypePolicy, error) {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, nd.InstanceID, tx)
		if err != nil {
			return err
		}
		inst = i
		if inst == nil {
			return nil
		}
		t, err := sb.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		tmpl = t
		return err
	}); err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, nil
	}
	if tmpl == nil {
		return nil, nil
	}
	for _, td := range tmpl.Spec.Nodes {
		if td.Type != nd.NodeType {
			continue
		}
		if p, ok := td.ErrorTypes[errorClass]; ok {
			// Copy so the caller can take its address without aliasing the map.
			cp := p
			return &cp, nil
		}
		return nil, nil
	}
	return nil, nil
}

// requiredStoresForNode resolves the node's required_stores list from
// the template's per-node-type definition. Used when re-enqueueing on
// retry so the rebooted dispatch row carries the same supervisor-pool
// predicate (`required_stores ⊆ accepted_stores`, spec §6.2) as the
// original. Returns nil when the template / node-def cannot be located —
// the queue treats nil and []string{} as equivalent (no required stores
// declared, accepted by every supervisor pool).
func requiredStoresForNode(ctx context.Context, sb persistence.Tables, nd *persistence.NodeRow) []string {
	var inst *persistence.InstanceRow
	var tmpl *persistence.TemplateRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := sb.Instances().Get(ctx, nd.InstanceID, tx)
		if err != nil || i == nil {
			return err
		}
		inst = i
		t, err := sb.Templates().GetByHash(ctx, i.TemplateHash, tx)
		if err != nil {
			return err
		}
		tmpl = t
		return nil
	}); err != nil {
		return nil
	}
	if inst == nil || tmpl == nil {
		return nil
	}
	for _, td := range tmpl.Spec.Nodes {
		if td.Type != nd.NodeType {
			continue
		}
		return node.RequiredStores(td)
	}
	return nil
}
