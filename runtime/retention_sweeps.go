// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// retention_sweeps.go — E10. Watchdog retention sweeps.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Retention. Two sweeps land here:
//
//   - SweepLineageRetention: delete rimsky_lineage rows older than the
//     retention window whose corresponding run / claim_handle has been
//     removed.
//
//   - SweepRunTreeRetention: project for the future — once the
//     destructive B3 migration drops the state columns from
//     rimsky_nodes and frame retention drops old runs, this sweep
//     deletes runs from frames older than `retention.recent_frames_kept`.
//     The skeleton lands here; the body wires up in a follow-up.
//
// @concept: retention
// @concept: frame
// @concept: lineage

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// RetentionConfig carries the durable retention parameters. Sourced
// from `cfg:retention` in rimsky.yml.
type RetentionConfig struct {
	// RecentFramesKept caps the per-instance frame backlog kept on disk.
	// Default 100; values <=0 disable the sweep.
	RecentFramesKept int
	// LineageTrailing is the trailing window for the lineage sweep.
	// Default 30 days; zero disables the sweep.
	LineageTrailing time.Duration
	// ClaimHandlesTrailing is the trailing window for the claim-handle
	// retention sweep. Default 30 days; zero disables the sweep.
	//
	// Post-Stage-3 of the claim-handle state-column refactor: terminal
	// claim-handle rows persist past the auto-terminal Promote until
	// this window elapses, then are reaped by
	// `SweepClaimHandleRetention`. Durable-committed rows (state=
	// 'committed' AND lifetime='durable') are exempt — they're the
	// asset surface, released only by `ReleaseHeldDurableClaims` or
	// the operator `DELETE /instances/{id}/assets/{alias}` handler.
	//
	// @concept: claim-handle
	// @concept: retention
	ClaimHandlesTrailing time.Duration

	// MessageIdempotenciesTrailing is the trailing window for the
	// rimsky_message_idempotencies sweep. Default 24h; zero disables
	// the sweep. Dedup rows reaped past the window allow a
	// long-deferred retry to land as a fresh message; this is the
	// intended behavior — dedup tokens are short-lived.
	//
	// @concept: message
	// @concept: retention
	MessageIdempotenciesTrailing time.Duration
}

// SweepLineageRetention runs `DeleteOlderThan` on the lineage table.
// Returns the number of rows deleted. Idempotent across multiple
// invocations.
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

// SweepRunTreeRetention deletes rimsky_node_runs rows belonging to
// frames older than the `RecentFramesKept`-th most-recent terminal
// frame per instance. Spec §Run-tree retention. Returns the number of
// rows deleted across all instances.
//
// Post-stage-1 lifecycle flip: terminal run rows survive past active
// terminal (RemoveForNodeInTx flips phase to a terminal value rather
// than DELETEing the row), so the retention sweep has terminal rows to
// prune. The actual delete predicate is owned by the persistence layer
// (`FrameTable.PruneOldRunsForRetention`) so SQLite + Postgres can
// implement the window query in their native dialect.
//
// Only terminal frames count toward the "keep N most-recent" cap —
// in-flight frames (queued/running) are exempt so a long-running
// instance can't have its in-flight work pruned out from under it.
func SweepRunTreeRetention(
	ctx context.Context, cfg RetentionConfig, tables persistence.Tables,
	_ time.Time, log shared.Logger,
) (int, error) {
	if cfg.RecentFramesKept <= 0 {
		return 0, nil
	}
	n, err := tables.Frames().PruneOldRunsForRetention(ctx, cfg.RecentFramesKept)
	if err != nil {
		return 0, fmt.Errorf("SweepRunTreeRetention: %w", err)
	}
	if log != nil && n > 0 {
		log.Info("retention.run_tree.sweep",
			"deleted", n, "recent_frames_kept", cfg.RecentFramesKept)
	}
	return n, nil
}
