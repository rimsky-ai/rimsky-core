// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: claim-handle
// @concept: claim-lifetime

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

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
