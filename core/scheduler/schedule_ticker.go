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

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
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
func ProcessSchedules(ctx context.Context, sb persistence.Store, disp MessageDispatcher, clock shared.Clock, log shared.Logger) (int, error) {
	due, err := sb.Schedules().DueBefore(ctx, clock.Now(), nil)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, row := range due {
		// Resolve the node so the instance_id can be attached to emitted
		// events (matches the pattern used by pure_cascade.go). Missing node
		// is not fatal — we still log the failure with a nil InstanceID.
		var instancePtr *shared.UUID
		if nd, nerr := sb.Nodes().Get(ctx, row.NodeID, nil); nerr == nil && nd != nil {
			inst := nd.InstanceID
			instancePtr = &inst
		}
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
			// Append schedule_dispatch_failed event (plan-added; see spec §11.2 + CHANGELOG).
			_ = sb.Events().Append(ctx, persistence.EventAppendInput{
				NodeID:     ptrUUID(row.NodeID),
				InstanceID: instancePtr,
				Kind:       "schedule_dispatch_failed",
				Payload: map[string]any{
					"node_id": row.NodeID.String(),
					"error":   err.Error(),
				},
			}, nil)
			continue
		}
		firedAt := clock.Now()
		if err := sb.Schedules().RecordFired(ctx, row.NodeID, next, firedAt, nil); err != nil {
			if log != nil {
				log.Warn("schedule RecordFired failed", "node_id", row.NodeID.String(), "error", err.Error())
			}
			_ = sb.Events().Append(ctx, persistence.EventAppendInput{
				NodeID:     ptrUUID(row.NodeID),
				InstanceID: instancePtr,
				Kind:       "schedule_dispatch_failed",
				Payload:    map[string]any{"node_id": row.NodeID.String(), "error": err.Error()},
			}, nil)
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
			_ = sb.Events().Append(ctx, persistence.EventAppendInput{
				NodeID:     ptrUUID(row.NodeID),
				InstanceID: instancePtr,
				Kind:       "schedule_dispatch_failed",
				Payload:    map[string]any{"node_id": row.NodeID.String(), "error": err.Error()},
			}, nil)
			continue
		}
		// Log the successful fire.
		_ = sb.Events().Append(ctx, persistence.EventAppendInput{
			NodeID:     ptrUUID(row.NodeID),
			InstanceID: instancePtr,
			Kind:       "schedule_fired",
			Payload: map[string]any{
				"node_id":   row.NodeID.String(),
				"cron_expr": row.CronExpr,
				"fired_at":  firedAt,
			},
		}, nil)
		fired++
	}
	return fired, nil
}

func ptrUUID(u shared.UUID) *shared.UUID { return &u }
