// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sweep_message_idempotencies.go — retention sweep over
// rimsky_message_idempotencies. Universal idempotency dedup rows expire
// after a configured trailing window (default 24h). A retry that arrives
// past the window is treated as a fresh message — dedup tokens are
// short-lived by design.
//
// Sibling to `SweepClaimHandleRetention`: serialized via the scheduler-
// tick advisory lock. No per-row claimant guard required — the rows
// have no holder.
//
// @concept: message

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// SweepMessageIdempotencies deletes idempotency rows older than
// `cfg.MessageIdempotenciesTrailing`. Returns the deleted-rows count.
// Disabled when the trailing duration is <= 0.
func SweepMessageIdempotencies(
	ctx context.Context, mit persistence.MessageIdempotencyTable, cfg RetentionConfig,
	now time.Time, log shared.Logger,
) (int64, error) {
	if cfg.MessageIdempotenciesTrailing <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-cfg.MessageIdempotenciesTrailing)
	n, err := mit.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("SweepMessageIdempotencies: %w", err)
	}
	if log != nil && n > 0 {
		log.Info("retention.message_idempotencies.sweep",
			"deleted", n,
			"cutoff", cutoff.Format(time.RFC3339),
			"trailing", cfg.MessageIdempotenciesTrailing.String())
	}
	return n, nil
}
