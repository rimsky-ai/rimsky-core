// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type ParkedSweepArgs struct {
	Persist      persistence.Tables
	Queue        persistence.Queue
	Clock        shared.Clock
	Logger       shared.Logger
	SupervisorID string
	Limit        int
	Metrics      MetricsHook
}

func SweepParkedNodes(ctx context.Context, args ParkedSweepArgs) error {
	if args.Persist == nil || args.Queue == nil || args.SupervisorID == "" {
		return nil
	}
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	now := args.Clock.Now()
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}

	ready, err := args.Queue.ListParkedReadyForResume(ctx, now, limit)
	if err != nil {
		log.Warn("SweepParkedNodes: ListParkedReadyForResume failed", "error", err.Error())
	}
	for _, row := range ready {
		wakeArgs := WakeParkedArgs{
			Persist:      args.Persist,
			Queue:        args.Queue,
			Logger:       log,
			TargetNodeID: row.NodeID,
			NodeRunID:    row.NodeRunID,
			SupervisorID: args.SupervisorID,
		}
		if args.Metrics != nil {
			args.Metrics.IncInvalidate("scheduler")
		}
		if err := WakeParkedNode(ctx, wakeArgs, WakeDeadlineElapsed); err != nil {
			log.Warn("SweepParkedNodes: wake failed",
				"node_id", row.NodeID.String(), "error", err.Error())
		}
	}
	return nil
}
