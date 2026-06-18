// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: frame
// @concept: lineage

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type RetentionConfig struct {
	RecentFramesKept int
	// @concept: frame
	// @concept: event-log
	TraceTrailing   time.Duration
	LineageTrailing time.Duration
	// @concept: claim-handle
	// @concept: claim-lifetime
	ClaimHandlesTrailing time.Duration

	// @concept: message
	MessageIdempotenciesTrailing time.Duration
}

func SweepLineageRetention(
	ctx context.Context, lt persistence.LineageTable, cfg RetentionConfig, now time.Time, log shared.Logger,
) (int, error) {
	if cfg.LineageTrailing <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-cfg.LineageTrailing)
	n, err := lt.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("SweepLineageRetention: %w", err)
	}
	if log != nil && n > 0 {
		log.Info("retention.lineage.sweep", "deleted", n, "cutoff", cutoff.Format(time.RFC3339))
	}
	return n, nil
}

func SweepRunTreeRetention(
	ctx context.Context, cfg RetentionConfig, tables persistence.Tables,
	now time.Time, log shared.Logger,
) (int, error) {
	if cfg.RecentFramesKept <= 0 && cfg.TraceTrailing <= 0 {
		return 0, nil
	}
	var cutoff time.Time
	if cfg.TraceTrailing > 0 {
		cutoff = now.Add(-cfg.TraceTrailing)
	}
	frames, err := tables.Frames().PruneTraceForRetention(ctx, cfg.RecentFramesKept, cutoff)
	if err != nil {
		return 0, fmt.Errorf("SweepRunTreeRetention: frames: %w", err)
	}
	if log != nil && frames > 0 {
		log.Info("retention.trace.frames.sweep",
			"deleted", frames, "recent_frames_kept", cfg.RecentFramesKept,
			"cutoff", cutoffLogValue(cutoff))
	}
	if cfg.TraceTrailing > 0 {
		events, err := tables.Events().DeleteOlderThan(ctx, cutoff)
		if err != nil {
			return frames, fmt.Errorf("SweepRunTreeRetention: events: %w", err)
		}
		if log != nil && events > 0 {
			log.Info("retention.trace.events.sweep",
				"deleted", events, "cutoff", cutoff.Format(time.RFC3339))
		}
	}
	return frames, nil
}

func cutoffLogValue(cutoff time.Time) string {
	if cutoff.IsZero() {
		return "none"
	}
	return cutoff.Format(time.RFC3339)
}
