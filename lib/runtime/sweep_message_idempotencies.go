// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

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
