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

// RetentionConfig carries the durable retention parameters. Sourced
// from `cfg:retention` in rimsky.yml.
type RetentionConfig struct {
	// RecentFramesKept caps the per-instance frame backlog kept on disk.
	// Default 100; values <=0 disable the sweep. This is the COUNT
	// dimension of trace retention (the most-recent-N-terminal-frames
	// cap); TraceTrailing is the TIME dimension. Reaping is the lesser of
	// the two — a frame is reaped when it is older than TraceTrailing OR
	// beyond the RecentFramesKept most-recent terminal frames.
	RecentFramesKept int
	// TraceTrailing is the trailing time window for the whole per-instance
	// execution trace — frames, their node_runs (via ON DELETE CASCADE),
	// and the time-keyed event logs (rimsky_events audit + rimsky_node_events
	// named). Default 30 days; <=0 disables the time dimension (then only
	// the RecentFramesKept count cap applies). The TIME dimension of trace
	// retention; reaping is the lesser of TraceTrailing and RecentFramesKept.
	//
	// @concept: frame
	// @concept: event-log
	TraceTrailing time.Duration
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
	// @concept: claim-lifetime
	ClaimHandlesTrailing time.Duration

	// MessageIdempotenciesTrailing is the trailing window for the
	// rimsky_message_idempotencies sweep. Default 24h; zero disables
	// the sweep. Dedup rows reaped past the window allow a
	// long-deferred retry to land as a fresh message; this is the
	// intended behavior — dedup tokens are short-lived.
	//
	// @concept: message
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

// SweepRunTreeRetention reaps the whole per-instance execution trace —
// terminal frame rows (cascading their node_runs) plus the time-keyed
// event logs — under one coherent retention policy. Returns the number of
// frame rows deleted across all instances.
//
// Three deletes run from one cutoff + count cap:
//
//   - Frames().PruneTraceForRetention reaps terminal frame rows older than
//     `cutoff` OR beyond the `RecentFramesKept` most-recent terminal frames
//     per instance (the lesser-of bound); their node_runs go via the
//     frame→node_run ON DELETE CASCADE. In-flight frames (queued/running,
//     including parked-held) are exempt — nothing live is reaped.
//   - Events().DeleteOlderThan reaps rimsky_events (audit log) rows older
//     than `cutoff`. Event logs are time-keyed (no frame FK), so the count
//     cap does NOT apply to them — only the trailing time window.
//
// The event sweep only runs when TraceTrailing > 0 (a zero cutoff is the
// "no time bound" sentinel; running an event delete against it would be a
// no-op at best and a full-table scan at worst). The frame reaper always
// runs when EITHER dimension is enabled — the count cap alone is a valid
// retention policy for structural rows even with no time window.
//
// Per TD-collapse-named-event-to-tags the rimsky_node_events ledger
// has retired; subscriber-visible discriminators ride as tags on the
// settling terminal verdict (concept:terminal-tag), so no separate
// named-event retention sweep is needed.
//
// Post-stage-1 lifecycle flip: terminal run rows survive past active
// terminal (RemoveForNodeInTx flips phase to a terminal value rather than
// DELETEing the row), so the cascade has rows to remove when its frame
// is reaped.
func SweepRunTreeRetention(
	ctx context.Context, cfg RetentionConfig, tables persistence.Tables,
	now time.Time, log shared.Logger,
) (int, error) {
	// @constraint: either retention dimension alone must reap. A config with
	// only trace_trailing set (no count cap) passes the scheduler gate; if
	// this guard required RecentFramesKept it would silently reap nothing —
	// that is the load-bearing "either dimension alone reaps" property.
	if cfg.RecentFramesKept <= 0 && cfg.TraceTrailing <= 0 {
		return 0, nil
	}
	// @constraint: a zero cutoff (time.Time{}) is the persistence layer's
	// "no time bound" sentinel; compute a real cutoff only when the time
	// dimension is enabled.
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
	// @constraint: event logs age out by the time window alone (no count
	// cap, no frame FK). Skip them entirely when the time dimension is
	// disabled.
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

// cutoffLogValue renders a cutoff for structured logging, surfacing the
// count-only case (zero cutoff = no time bound) as "none" rather than the
// 0001 zero-time string.
func cutoffLogValue(cutoff time.Time) string {
	if cutoff.IsZero() {
		return "none"
	}
	return cutoff.Format(time.RFC3339)
}
