// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	clock := args.Clock
	if clock == nil {
		clock = shared.SystemClock{}
	}
	now := clock.Now()
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}

	ready, err := args.Queue.ListParkedReadyForResume(ctx, now, limit)
	if err != nil {
		log.Warn("PARKEDNODE.RESUMELIST.FAILED", "site", "SweepParkedNodes", "error", err.Error())
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
			log.Warn("PARKEDNODE.WAKE.FAILED", "site", "SweepParkedNodes",
				"node_id", row.NodeID.String(), "error", err.Error())
		}
	}
	return nil
}
