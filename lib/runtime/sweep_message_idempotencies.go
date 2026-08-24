// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
		log.Info("RETENTION.MESSAGEIDEMPOTENCIES.SWEPT",
			"deleted", n,
			"cutoff", cutoff.Format(time.RFC3339),
			"trailing", cfg.MessageIdempotenciesTrailing.String())
	}
	return n, nil
}
