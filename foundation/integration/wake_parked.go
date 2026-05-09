// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unified invalidate path: wakeParkedNode shared by E3 (SweepParkedNodes
// time-based wake), G3 (admin endpoint POST
// /admin/instances/{instance}/nodes/{node_id}/invalidate), and H2
// (on_event-handler-emitted invalidates). The single helper dispatches
// by node state:
//
//   - parked: re-queues for resume dispatch via wakeParkedNode (transitions
//     phase parked→pending so any eligible supervisor can pick the row up,
//     and transitions node state parked→stale so the standard ready sweep
//     re-dispatches it).
//   - fresh:  standard invalidate via InvalidateNode (frame engine).
//   - running: rejected — caller decides to surface the conflict.
//   - failed/stale: standard invalidate (a stale row that's been re-run
//     races; today's behavior is preserved by routing to InvalidateNode).
//
// Per the 2026-05-08 platform-extensions plan E3/E4/G3/H2.

package integration

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// ErrInvalidateRunning is returned by the unified invalidate handler
// when the target is in `running` state. Callers (admin G3) translate
// this to HTTP 409.
var ErrInvalidateRunning = errors.New("invalidate: target node is running")

// WakeReason captures the invalidate's source for the resume_reason
// field of ResumeContext (per plan E4). Distinct values let the executor
// log / tune behavior on resume.
type WakeReason string

const (
	// WakeDeadlineElapsed is set by SweepParkedNodes when resume_at
	// elapses.
	WakeDeadlineElapsed WakeReason = "deadline_elapsed"
	// WakeExternalInvalidate is set by the admin invalidate endpoint
	// and by handler-emitted invalidates (H2).
	WakeExternalInvalidate WakeReason = "external_invalidate"
)

// UnifiedInvalidate is the entry point shared by all sources of an
// invalidate request. The function:
//
//   1. Loads the target's current state via Nodes().Get.
//   2. If parked: dispatches the wakeParkedNode path.
//   3. If running: returns ErrInvalidateRunning so the caller can
//      surface a 409 (admin) or log-and-skip (handler).
//   4. Otherwise: routes to InvalidateNode (the standard frame engine).
//
// The supervisorID is required for the parked branch (claims the row);
// the standard InvalidateNode path leaves the SupervisorID empty
// (existing behavior).
func UnifiedInvalidate(ctx context.Context, args InvalidateArgs, supervisorID string, reason WakeReason) error {
	if args.Persist == nil {
		return errors.New("UnifiedInvalidate: Persist required")
	}
	if args.Queue == nil {
		return errors.New("UnifiedInvalidate: Queue required")
	}
	target, err := loadTargetNode(ctx, args.Persist, args.TargetNodeID)
	if err != nil {
		return err
	}
	if target == nil {
		// Target not found — nothing to do.
		if args.Logger != nil {
			args.Logger.Debug("UnifiedInvalidate: target not found",
				"node_id", args.TargetNodeID.String())
		}
		return nil
	}
	switch target.State {
	case shared.NodeStateParked:
		if supervisorID == "" {
			return errors.New("UnifiedInvalidate: supervisorID required for parked branch")
		}
		return wakeParkedNode(ctx, args, target, supervisorID, reason)
	case shared.NodeStateRunning:
		return ErrInvalidateRunning
	default:
		// fresh / stale / failed → frame-engine invalidate.
		return InvalidateNode(ctx, args)
	}
}

// wakeParkedNode runs the parked-node wake. Transitions phase
// parked→pending (claimed_by reset to NULL so any eligible supervisor
// can pick the row up; the supervisorID is recorded in the audit-log
// row only), transitions node state parked→stale, audit-logs the
// resume.
//
// The actual re-dispatch happens on the next supervisor tick — the
// runner's SelectCandidates picks up phase='pending' rows and the
// standard ready sweep re-enqueues stale nodes.
func wakeParkedNode(ctx context.Context, args InvalidateArgs, target *persistence.NodeRow, supervisorID string, reason WakeReason) error {
	parked, err := args.Queue.GetParkedByNode(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("wakeParkedNode: GetParkedByNode: %w", err)
	}
	if parked == nil {
		// Race: the node is parked but the worker_request row has been
		// reaped or the parked status is in transition.
		if args.Logger != nil {
			args.Logger.Warn("wakeParkedNode: no parked worker_request row found",
				"node_id", target.ID.String())
		}
		return nil
	}
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		resumed, err := args.Queue.ResumeParkedInTx(ctx, tx, parked.DispatchID, supervisorID, string(reason))
		if err != nil {
			return err
		}
		if !resumed {
			// Row already moved out of parked (raced); nothing to do.
			return nil
		}
		if err := args.Persist.Nodes().UpdateState(ctx, target.ID,
			shared.NodeStateStale, cascade.ReasonHandlerResume, "", tx); err != nil {
			return err
		}
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &target.ID, InstanceID: &target.InstanceID,
			Kind: "parked_resume_started",
			Payload: map[string]any{
				"resume_reason": string(reason),
				"supervisor_id": supervisorID,
				"prior_reason":  parked.Reason,
				"had_session":   parked.SessionToken != "",
				"payload_bytes": len(parked.PayloadInline) + len(parked.PayloadHandle),
			},
		}, tx)
	})
}

// loadTargetNode wraps Persist.Transaction + Nodes().Get for the
// state-dispatch read.
func loadTargetNode(ctx context.Context, persist persistence.Store, id shared.UUID) (*persistence.NodeRow, error) {
	var out *persistence.NodeRow
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := persist.Nodes().Get(ctx, id, tx)
		out = row
		return err
	})
	return out, err
}

// InvalidateHandler is a thin adapter that wraps UnifiedInvalidate
// in the {InvalidateNode(ctx, instanceID, nodeID) (any, error)} shape
// the control-api admin handler (G3) uses. Returned values are
// human-readable result objects; on a parked-state invalidate, the
// returned shape lists the wake reason and the resumed dispatch row.
//
// Wiring: the control-api process constructs one of these at startup
// with its persistence + queue handles and the operator-id, then sets
// it as AppDeps.InvalidateHandler before mounting routes.
type InvalidateHandler struct {
	Persist      persistence.Store
	Queue        persistence.Queue
	Clock        shared.Clock
	Logger       shared.Logger
	SupervisorID string
	// Metrics threads the dispatch/invalidate instrumentation hook
	// through to InvalidateNode so admin-endpoint invalidates increment
	// `rimsky_invalidates_total{source="admin"}`. Nil → no-op.
	Metrics MetricsHook
}

// InvalidateNode implements the modeling/controlapi.InvalidateHandler
// interface (without taking the import — the structural type-match
// shape kicks in at the modeling-layer wire site).
func (h *InvalidateHandler) InvalidateNode(ctx context.Context, instanceID, nodeID string) (any, error) {
	id, err := parseNodeUUID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("invalidate: parse node_id: %w", err)
	}
	ia := InvalidateArgs{
		Persist:      h.Persist,
		Queue:        h.Queue,
		Clock:        h.Clock,
		Logger:       h.Logger,
		TargetNodeID: id,
		Reason:       "admin_invalidate",
		SupervisorID: h.SupervisorID,
		Frame:        "next",
		Metrics:      h.Metrics,
	}
	if err := UnifiedInvalidate(ctx, ia, h.SupervisorID, WakeExternalInvalidate); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":      "accepted",
		"instance_id": instanceID,
		"node_id":     nodeID,
	}, nil
}

// parseNodeUUID is a small helper for the admin-handler adapter.
func parseNodeUUID(s string) (shared.UUID, error) {
	if s == "" {
		return shared.UUID{}, errors.New("empty node_id")
	}
	return uuid.Parse(s)
}
