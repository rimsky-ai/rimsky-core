// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sweep_claim_handle_retention.go — Stage 3 of the claim-handle
// state-column refactor. Retention sweep over terminal
// `rimsky_claim_handles` rows.
//
// Spec
// .ok-planner/specs/2026-05-17-post-data-platform-cleanup-design.md
// §Claim-handle state-column refactor.
//
// Sibling to `SweepLineageRetention`: a time-based cutoff sweep that
// reaps rows whose `resolved_at` is older than the configured trailing
// window. Differs from the orphan reaper (which keys on heartbeat
// staleness, fires `ClaimProducer.Abandon` for the bail path, and
// operates on active rows) — this sweep operates on terminal rows
// only and fires no producer verbs.
//
// Serialization: relies on the scheduler-tick advisory lock. No
// per-row claimant guard is needed (the rows being swept have
// `holder_supervisor_id IS NULL` by construction — the post-Stage-4
// CHECK constraint nulls the column whenever `state` exits `'active'`).
//
// Never sweeps durable-committed rows (state='committed' AND
// lifetime='durable') — those are the asset surface; released only by
// `ReleaseHeldDurableClaims` (instance termination) or the operator
// `DELETE /instances/{id}/assets/{alias}` handler.
//
// @blessed-invariant 4 (post-refactor): non-active-row deletions are
// guarded by absence + the scheduler-tick advisory lock.
// @blessed-invariant 22: held-durable claim handles persist across
// instance dispatches.
// @concept: claim-handle
// @concept: claim-lifetime

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// SweepClaimHandleRetention deletes terminal `rimsky_claim_handles`
// rows whose `resolved_at` is older than `cfg.ClaimHandlesTrailing`,
// excluding durable-committed rows (asset surface). Returns the
// deleted-rows count. Idempotent across multiple invocations.
//
// Disabled when `cfg.ClaimHandlesTrailing <= 0`.
func SweepClaimHandleRetention(
	ctx context.Context, ct persistence.ClaimHandleTable, cfg RetentionConfig,
	now time.Time, log shared.Logger,
) (int, error) {
	if cfg.ClaimHandlesTrailing <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-cfg.ClaimHandlesTrailing)
	n, err := ct.DeleteResolvedOlderThan(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("SweepClaimHandleRetention: %w", err)
	}
	if log != nil && n > 0 {
		log.Info("retention.claim_handles.sweep",
			"deleted", n,
			"cutoff", cutoff.Format(time.RFC3339),
			"trailing", cfg.ClaimHandlesTrailing.String())
	}
	return n, nil
}
