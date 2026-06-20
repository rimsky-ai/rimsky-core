// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: cascade
package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: cascade
// @concept: wait-set
// @story: upstream-pull-on-invalidate
func pullUpstreamRefreshesForNode(
	ctx context.Context, sb persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	logger shared.Logger,
	senderNodeID, instanceID, frameID shared.UUID,
) error {
	args := RunArgs{Persist: sb, Queue: queue, Logger: logger}
	n, err := sb.Nodes().Get(ctx, senderNodeID, tx)
	if err != nil || n == nil {
		return err
	}
	if n.RunScopeID == nil {
		return nil
	}
	senderRunID, ok, err := queue.GetInFlightRunForNode(ctx, tx, senderNodeID, *n.RunScopeID)
	if err != nil {
		return fmt.Errorf("pullUpstreamRefreshesForNode: resolve sender run: %w", err)
	}
	if !ok {
		return nil
	}
	inst, err := sb.Instances().Get(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("pullUpstreamRefreshesForNode: get instance: %w", err)
	}
	if inst == nil {
		return nil
	}
	instNodes, err := sb.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("pullUpstreamRefreshesForNode: list instance nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, in := range instNodes {
		byType[in.NodeType] = append(byType[in.NodeType], in)
	}
	visited := map[shared.UUID]struct{}{senderNodeID: {}}
	return pullForceRefreshUpstreams(
		ctx, args, tx, *n, byType,
		senderRunID, *n.RunScopeID, frameID,
		inst.TemplateHash, visited,
	)
}

// @concept: cascade
// @concept: attribute
func stalemarkAndEnqueueInFrame(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	target *persistence.NodeRow, targetRunScopeID shared.UUID, frameID shared.UUID,
) error {
	runID, ok, err := args.Queue.GetInFlightRunForNode(ctx, tx, target.ID, targetRunScopeID)
	if err != nil {
		return fmt.Errorf("stalemarkAndEnqueueInFrame: resolve in-flight run %s: %w", target.ID, err)
	}
	if !ok {
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
	return pullUpstreamRefreshesForNode(ctx, args.Persist, args.Queue, tx,
		args.Logger, target.ID, target.InstanceID, frameID)
}
