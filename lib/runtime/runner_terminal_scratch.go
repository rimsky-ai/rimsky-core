// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: executor

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: executor
// @decision: scratch-protocol
// @decision: scratch-column
func applyTerminalScratchInTx(
	ctx context.Context, args RunArgs, acq *acquisition, scratch []byte, tx persistence.Tx,
) error {
	if len(scratch) == 0 {
		return nil
	}
	if isSubgraphExitNode(acq) {
		return nil
	}
	if err := args.Queue.WriteScratch(ctx, acq.NodeRunID, scratch, tx); err != nil {
		return fmt.Errorf("applyTerminalScratchInTx: %w", err)
	}
	now := time.Now().UTC()
	if args.Clock != nil {
		now = args.Clock.Now().UTC()
	}
	// @decision: writeback-bumps-progress
	if _, err := args.Queue.BumpLastProgressAt(ctx, acq.NodeRunID, now, tx); err != nil {
		return fmt.Errorf("applyTerminalScratchInTx: bump last_progress_at: %w", err)
	}
	return nil
}
