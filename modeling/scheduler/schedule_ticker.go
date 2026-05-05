// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package scheduler schedule_ticker.go — cron-driven schedule fires.
//
// Missed-fire policy: cron advancement is based on the row's prior
// `next_fire_at`, NOT `clock.Now()`. This means a long outage does NOT
// backfill missed fires — the scheduler produces a single fire on recovery
// and resumes on-rhythm from the next natural cron slot after the prior
// next_fire_at. Rationale: backfilling a 6-hour outage for an hourly
// schedule would generate a thundering herd of six identical-payload runs
// with only their timestamps differing; the intent of cron-backed
// invalidation is freshness, which a single post-outage fire satisfies.
package scheduler

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// MessageDispatcher is the narrow interface the schedule ticker needs to
// route invalidates. scheduler.InvalidateNode (Task 9.1) satisfies this.
type MessageDispatcher interface {
	EmitInvalidate(ctx context.Context, req InvalidateRequest) error
}

// InvalidateRequest is the payload forwarded to the message dispatcher when
// a schedule fires. SourceNodeID is nil for scheduler-originated invalidates
// because the schedule is a property of the target node itself.
type InvalidateRequest struct {
	SourceNodeID *shared.UUID
	TargetNodeID shared.UUID
	Reason       string
}

// NextFireAt returns the next cron fire time strictly after `after`. Uses
// robfig/cron/v3 ParseStandard (5-field cron, UTC).
func NextFireAt(expr string, after time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(after), nil
}

// ProcessSchedules finds schedules with next_fire_at <= clock.Now(), emits
// invalidate for each target, and updates next_fire_at + last_fired_at.
// Errors in one row do not block others — each is logged and processing
// continues. Returns the number of schedules fired (even if some of their
// invalidates errored).
//
// Persistence access goes through Persist.Transaction wrappers because
// the underlying Store methods require an explicit tx (option C / no
// nil-tx). Each per-row read/write pair runs in its own short tx so a
// failed row doesn't roll back its neighbours; the out-of-band
// EmitInvalidate runs between the firing-state-write tx and the
// success-event tx.
func ProcessSchedules(ctx context.Context, sb persistence.Store, disp MessageDispatcher, clock shared.Clock, log shared.Logger) (int, error) {
	var due []persistence.ScheduleRow
	if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		d, err := sb.Schedules().DueBefore(ctx, clock.Now(), tx)
		if err != nil {
			return err
		}
		due = d
		return nil
	}); err != nil {
		return 0, err
	}
	fired := 0
	for _, row := range due {
		instancePtr := lookupInstanceForNode(ctx, sb, row.NodeID)
		// Advance from the row's prior next_fire_at (not wall-clock). See
		// package doc for missed-fire rationale: after an outage we produce
		// a single fire and resume on-rhythm, not a backfill burst.
		next, err := NextFireAt(row.CronExpr, row.NextFireAt)
		if err != nil {
			if log != nil {
				log.Warn("schedule cron invalid; skipping",
					"node_id", row.NodeID.String(),
					"cron", row.CronExpr,
					"error", err.Error())
			}
			appendDispatchFailed(ctx, sb, row.NodeID, instancePtr, err)
			continue
		}
		firedAt := clock.Now()
		if err := sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return sb.Schedules().RecordFired(ctx, row.NodeID, next, firedAt, tx)
		}); err != nil {
			if log != nil {
				log.Warn("schedule RecordFired failed", "node_id", row.NodeID.String(), "error", err.Error())
			}
			appendDispatchFailed(ctx, sb, row.NodeID, instancePtr, err)
			continue
		}
		// Emit invalidate to the node itself. Downstream cascade follows.
		if err := disp.EmitInvalidate(ctx, InvalidateRequest{
			SourceNodeID: nil, // scheduler-originated
			TargetNodeID: row.NodeID,
			Reason:       "schedule_fired",
		}); err != nil {
			if log != nil {
				log.Warn("schedule emit invalidate failed", "node_id", row.NodeID.String(), "error", err.Error())
			}
			appendDispatchFailed(ctx, sb, row.NodeID, instancePtr, err)
			continue
		}
		// Log the successful fire.
		_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return sb.Events().Append(ctx, persistence.EventAppendInput{
				NodeID:     ptrUUID(row.NodeID),
				InstanceID: instancePtr,
				Kind:       "schedule_fired",
				Payload: map[string]any{
					"node_id":   row.NodeID.String(),
					"cron_expr": row.CronExpr,
					"fired_at":  firedAt,
				},
			}, tx)
		})
		fired++
	}
	return fired, nil
}

// lookupInstanceForNode reads the node row to extract its instance_id,
// or returns nil when the node is missing. Best-effort — a missing
// node is not fatal at the schedule-fire layer.
func lookupInstanceForNode(ctx context.Context, sb persistence.Store, nodeID shared.UUID) *shared.UUID {
	var out *shared.UUID
	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nd, err := sb.Nodes().Get(ctx, nodeID, tx)
		if err == nil && nd != nil {
			inst := nd.InstanceID
			out = &inst
		}
		return nil
	})
	return out
}

// appendDispatchFailed writes a schedule_dispatch_failed event in its
// own short tx. Errors are swallowed — failing to log a failure is
// not worth aborting the schedule-tick over.
func appendDispatchFailed(ctx context.Context, sb persistence.Store, nodeID shared.UUID, instancePtr *shared.UUID, cause error) {
	_ = sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sb.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     ptrUUID(nodeID),
			InstanceID: instancePtr,
			Kind:       "schedule_dispatch_failed",
			Payload: map[string]any{
				"node_id": nodeID.String(),
				"error":   cause.Error(),
			},
		}, tx)
	})
}

func ptrUUID(u shared.UUID) *shared.UUID { return &u }
