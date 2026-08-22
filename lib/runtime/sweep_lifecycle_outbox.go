// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: lifecycle-subscriber

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: lifecycle-subscriber-at-least-once-delivery
func SweepLifecycleOutbox(
	ctx context.Context, tables persistence.Tables, cfg RetentionConfig,
	now time.Time, log shared.Logger,
) (int64, error) {
	if cfg.LifecycleOutboxTrailing <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-cfg.LifecycleOutboxTrailing)
	var n int64
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		deleted, err := tables.LifecycleOutbox().DeleteOlderThan(ctx, cutoff, tx)
		n = deleted
		return err
	}); err != nil {
		return 0, fmt.Errorf("SweepLifecycleOutbox: %w", err)
	}
	if log != nil && n > 0 {
		log.Info("retention.lifecycle_outbox.sweep",
			"deleted", n,
			"cutoff", cutoff.Format(time.RFC3339),
			"trailing", cfg.LifecycleOutboxTrailing.String())
	}
	return n, nil
}
