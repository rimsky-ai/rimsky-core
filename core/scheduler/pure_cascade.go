// Pure-cascade sweep: finds stale nodes with no executor whose deps are all
// fresh, and transitions them to fresh inline (without going through the
// dispatch queue). Emits recalculate to each dependent so propagation
// continues. Per spec §6.1 step 3 / §6.4 — pure-cascade nodes never enqueue
// and never touch the supervisor; a single `pure_cascade_commit` event is
// logged, and the commit verdict is implicitly `changed=true` (propagation
// is the whole point; this is the v1 `non_resource_commit` renamed — see
// CHANGELOG for the rationale).
package scheduler

import (
	"context"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// PureCascadeArgs bundles the dependencies ProcessPureCascade needs.
type PureCascadeArgs struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
	Clock   shared.Clock
	Logger  shared.Logger
}

// ProcessPureCascade finds pure-cascade nodes ready to transition
// (Executor == "" AND state == stale AND all deps fresh) and transitions
// each to fresh inline under reason `pure_cascade`. For each transitioned
// node it appends a `pure_cascade_commit` event and invokes RecalculateNode
// on every dependent (propagation is the point; changed=true is implicit).
//
// Errors on individual nodes are logged and processing continues; the return
// value is the count of nodes successfully transitioned. Per spec §6.4.
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
		if err := sb.Nodes().UpdateState(ctx, n.ID, shared.NodeStateFresh, node.ReasonPureCascade, nil); err != nil {
			log.Warn("ProcessPureCascade: state transition failed",
				"node_id", n.ID.String(), "error", err.Error())
			continue
		}
		// Log pure_cascade_commit (spec §11.2; renamed from v1 non_resource_commit).
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
		// Emit recalculate to each dependent (changed=true by definition).
		dependents, derr := sb.Nodes().ListDependentsOf(ctx, n.ID, nil)
		if derr != nil {
			log.Warn("ProcessPureCascade: list dependents failed",
				"node_id", n.ID.String(), "error", derr.Error())
			count++
			continue
		}
		for _, dep := range dependents {
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
		count++
	}
	return count, nil
}
