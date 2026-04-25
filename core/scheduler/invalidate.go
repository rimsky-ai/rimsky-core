// Package scheduler port of rimsky/src/scheduler/invalidate.ts and
// recalculate.ts. Pure functions over storage.StorageBackend +
// queue.DispatchQueue + shared.Clock + shared.Logger.
package scheduler

import (
	"context"
	"strings"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// isOperatorInvalidate recognizes the reason strings that operators produce
// ("operator", "operator_override", or anything prefixed "operator"). The
// prefix form lets callers narrow the provenance (e.g. "operator_reset",
// "operator_test_rerun") without needing to enumerate every spelling here.
func isOperatorInvalidate(reason string) bool {
	return strings.HasPrefix(reason, "operator")
}

// InvalidateArgs is the payload for InvalidateNode.
type InvalidateArgs struct {
	Storage        storage.StorageBackend
	Queue          queue.DispatchQueue
	Clock          shared.Clock
	Logger         shared.Logger
	SourceNodeID   *shared.UUID
	TargetNodeID   shared.UUID
	Reason         string
	RestoreVersion string // "" or "previous" or a version UUID string
	// SupervisorID, when set, claimant-guards RemoveForNode so the invalidate
	// path can't drop a dispatch row that belongs to a different supervisor.
	// Callers originating from the scheduler tick / cron fire leave this
	// empty (no supervisor is holding the row then).
	SupervisorID string
}

// InvalidateNode routes an invalidate message to TargetNodeID per spec §6
// and TS rimsky/src/scheduler/invalidate.ts. Flow:
//  1. Append message_emitted (source) + message_received (target) events.
//  2. Load target node.
//  3. If RestoreVersion is set and the target owns a resource that supports
//     restore, call RestoreVersion on the resource(s); emit recalculate to
//     dependents; set node state = fresh via reason=restore_version; return.
//  4. If node is already stale or running, no-op (idempotent).
//  5. Else transition fresh→stale, emit invalidate to all dependents
//     (recursive), remove any pending dispatch for this node (the node is
//     now stale, so its prior claim is moot), and let the scheduler's next
//     tick's ready-sweep re-enqueue when deps are satisfied.
func InvalidateNode(ctx context.Context, args InvalidateArgs) error {
	sb, log := args.Storage, args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}

	// Emit + receive events.
	params := map[string]any{
		"reason": args.Reason,
	}
	if args.RestoreVersion != "" {
		params["restore_version"] = args.RestoreVersion
	}
	sourceStr := "(external)"
	if args.SourceNodeID != nil {
		sourceStr = args.SourceNodeID.String()
	}
	_ = sb.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &args.TargetNodeID,
		Kind:   "message_emitted",
		Payload: map[string]any{
			"type":           "invalidate",
			"source_node_id": sourceStr,
			"target_node_id": args.TargetNodeID.String(),
			"params":         params,
		},
	}, nil)
	_ = sb.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &args.TargetNodeID,
		Kind:   "message_received",
		Payload: map[string]any{
			"type":           "invalidate",
			"source_node_id": sourceStr,
			"target_node_id": args.TargetNodeID.String(),
			"params":         params,
		},
	}, nil)

	// Load target.
	target, err := sb.Nodes().Get(ctx, args.TargetNodeID, nil)
	if err != nil {
		return err
	}
	if target == nil {
		log.Warn("InvalidateNode: target not found", "node_id", args.TargetNodeID.String())
		return nil
	}

	// Restore-version path.
	if args.RestoreVersion != "" {
		if err := invalidateRestorePath(ctx, sb, args, target, log); err != nil {
			// Signals that the caller should fall through to the normal
			// invalidate path because the restore itself was not successfully
			// applied to a resource. The restore path returns a sentinel nil
			// in that case; any other error propagates.
			return err
		}
		return nil
	}

	// Normal invalidate path.
	// Idempotent — already stale: no-op.
	if target.State == shared.NodeStateStale {
		return nil
	}
	// Running nodes are normally left alone (the supervisor is in-flight and
	// will produce a terminal outcome). The exception is an *operator*
	// invalidate: operators explicitly asked for a rerun, so set
	// kill_requested on the node. The supervisor's heartbeat tick picks this
	// up on the next loop and cancels the subprocess; the normal
	// terminal_outcome path then transitions the node to stale and the
	// ready-sweep re-enqueues. Documented in operator-guide §invalidate.
	if target.State == shared.NodeStateRunning {
		if isOperatorInvalidate(args.Reason) {
			if err := sb.Nodes().SetKillRequested(ctx, target.ID, true, nil); err != nil {
				log.Warn("InvalidateNode: SetKillRequested failed",
					"node_id", target.ID.String(), "error", err.Error())
			}
		}
		return nil
	}

	// Transition to stale. Pick the appropriate reason.
	reason := node.ReasonInvalidateReceived
	if isOperatorInvalidate(args.Reason) {
		reason = node.ReasonOperatorInvalidate
	}
	if err := sb.Nodes().UpdateState(ctx, target.ID, shared.NodeStateStale, reason, nil); err != nil {
		return err
	}
	// Remove any pending dispatch row for this node (its prior claim is moot).
	_ = args.Queue.RemoveForNode(ctx, target.ID, args.SupervisorID)

	// Cascade: invalidate all dependents.
	dependents, err := sb.Nodes().ListDependentsOf(ctx, target.ID, nil)
	if err != nil {
		return err
	}
	for _, dep := range dependents {
		_ = InvalidateNode(ctx, InvalidateArgs{
			Storage:      sb,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       log,
			SourceNodeID: &target.ID,
			TargetNodeID: dep.ID,
			Reason:       "cascade_from_" + target.ID.String(),
		})
	}
	return nil
}

// invalidateRestorePath handles the restore-version branch of InvalidateNode.
// Returns nil on success. If a resource restore fails, logs a warning and
// returns nil (best-effort; the caller does NOT fall through in the Go port —
// matching TS behavior where a failed restore is swallowed but the target is
// still transitioned back to fresh and dependents notified).
func invalidateRestorePath(
	ctx context.Context,
	sb storage.StorageBackend,
	args InvalidateArgs,
	target *storage.NodeRow,
	log shared.Logger,
) error {
	resources, err := sb.Resources().ListByOwner(ctx, target.ID, nil)
	if err != nil {
		return err
	}
	for _, r := range resources {
		targetKind := "previous"
		var targetID shared.UUID
		if args.RestoreVersion != "previous" {
			id, perr := parseUUIDImpl(args.RestoreVersion)
			if perr != nil {
				log.Warn("InvalidateNode: invalid RestoreVersion UUID; skipping resource",
					"node_id", target.ID.String(),
					"resource_id", r.ID.String(),
					"rv", args.RestoreVersion)
				continue
			}
			targetKind = "id"
			targetID = id
		}
		if _, rerr := sb.Resources().RestoreVersion(ctx, r.ID, targetKind, targetID, nil); rerr != nil {
			log.Warn("InvalidateNode: restore failed; continuing best-effort",
				"resource_id", r.ID.String(), "error", rerr.Error())
			// Best-effort: continue with remaining resources.
		}
	}

	// Transition to fresh via restore_version reason.
	if err := sb.Nodes().UpdateState(ctx, target.ID, shared.NodeStateFresh, node.ReasonRestoreVersion, nil); err != nil {
		log.Warn("InvalidateNode: restore state transition failed",
			"node_id", target.ID.String(), "error", err.Error())
		return err
	}

	// Emit recalculate to dependents (they see the restored data as new).
	dependents, err := sb.Nodes().ListDependentsOf(ctx, target.ID, nil)
	if err != nil {
		return err
	}
	for _, dep := range dependents {
		_ = RecalculateNode(ctx, RecalculateArgs{
			Storage:      sb,
			Queue:        args.Queue,
			Clock:        args.Clock,
			Logger:       log,
			SourceNodeID: &target.ID,
			TargetNodeID: dep.ID,
		})
	}
	return nil
}
